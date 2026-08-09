package artindex

// Does augmenting the REFERENCE side close the capture-to-reference domain gap?
//
// Measured 2026-08-08: the hash separates hand-held captures from each other
// cleanly (same card ≤68 bits, different cards ≥104 — zero overlap over 595
// pairs) and fails to separate a capture from a clean Scryfall scan (own card
// ≤112, different card ≥100 — a 12-bit overlap). The hash is fine; the two
// sides look different.
//
// So: hash several degraded variants of each reference and keep the minimum
// distance. If a variant family resembles what a phone camera actually does to
// a card, the own-card distance drops and the overlap closes. Each family is
// measured separately, because a family that does nothing is a family not
// worth paying for on every one of 107k printings.
//
// Gated: HOARD_AUGMENT=1. Needs crops in HOARD_CROPS (one PNG per still id,
// as emitted by `cardkit-probe --emit-card`) and fetches ~20 reference images.

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/catalog"
)

// ---- Augmentations. Each models one thing a hand-held capture does that a
// flatbed scan does not. ----

// blur is a separable box blur of the given radius in source pixels. Models
// defocus and hand shake.
func blur(img image.Image, radius int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if radius < 1 || w == 0 || h == 0 {
		return img
	}
	get := func(x, y int) (float64, float64, float64) {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return float64(r), float64(g), float64(bl)
	}
	tmpR := make([]float64, w*h)
	tmpG := make([]float64, w*h)
	tmpB := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb float64
			n := 0
			for dx := -radius; dx <= radius; dx++ {
				sx := x + dx
				if sx < 0 || sx >= w {
					continue
				}
				r, g, bl := get(sx, y)
				sr, sg, sb = sr+r, sg+g, sb+bl
				n++
			}
			tmpR[y*w+x], tmpG[y*w+x], tmpB[y*w+x] = sr/float64(n), sg/float64(n), sb/float64(n)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb float64
			n := 0
			for dy := -radius; dy <= radius; dy++ {
				sy := y + dy
				if sy < 0 || sy >= h {
					continue
				}
				sr += tmpR[sy*w+x]
				sg += tmpG[sy*w+x]
				sb += tmpB[sy*w+x]
				n++
			}
			dst.Set(x, y, color.RGBA64{
				R: clamp16(sr / float64(n)), G: clamp16(sg / float64(n)),
				B: clamp16(sb / float64(n)), A: 0xffff})
		}
	}
	return dst
}

func clamp16(v float64) uint16 {
	if v < 0 {
		return 0
	}
	if v > 65535 {
		return 65535
	}
	return uint16(v)
}

// glare compresses the highlights non-linearly, leaving the shadows alone.
// Models what foil actually does: a specular sheen that clips the bright parts
// of the picture toward white while the dark parts keep their structure.
//
// Non-linear on purpose — see flatten below for why a linear version cannot
// possibly help.
func glare(img image.Image, k float64) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	soft := func(v uint32) uint16 {
		x := float64(v) / 65535
		// Pull everything toward 1.0, hardest at the top end.
		return clamp16(65535 * (x + (1-x)*k*x*x))
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dst.Set(x, y, color.RGBA64{R: soft(r), G: soft(g), B: soft(bl), A: 0xffff})
		}
	}
	return dst
}

// flatten scales contrast about the mid-tone — a LINEAR change.
//
// It is kept only because it pins down a property worth knowing: this hash is
// completely invariant to it. Every kept DCT coefficient is scaled by the same
// k, so every coefficient's position relative to their median is unchanged and
// the bits come out identical. Together with the DC term being dropped (which
// makes brightness free), that means **linear exposure and contrast changes
// cannot move this hash at all**.
//
// Which retires a theory: "foil glare washes out the art and eats the margins"
// cannot be the mechanism unless the washing is non-linear. See glare above.
func flatten(img image.Image, k float64) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			mid := 32768.0
			dst.Set(x, y, color.RGBA64{
				R: clamp16(mid + (float64(r)-mid)*k),
				G: clamp16(mid + (float64(g)-mid)*k),
				B: clamp16(mid + (float64(bl)-mid)*k),
				A: 0xffff})
		}
	}
	return dst
}

// cast tints the image. Models a desk lamp; it matters because the hash reads
// luma, and luma is a weighted mix of the three channels.
func cast(img image.Image, kr, kg, kb float64) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dst.Set(x, y, color.RGBA64{
				R: clamp16(float64(r) * kr), G: clamp16(float64(g) * kg),
				B: clamp16(float64(bl) * kb), A: 0xffff})
		}
	}
	return dst
}

// jitter re-frames the card: a sub-image window inset by (dx, dy) as a
// fraction of each axis, rescaled back to the original size.
//
// This models the one difference that is certain to exist. FromCard takes a
// FIXED FRACTION of whatever it is handed, and the reference is a card edge to
// edge at ratio 0.716 while a probe flatten measures 0.667-0.689 — so the two
// sides' crop windows do not land on the same part of the picture.
func jitter(img image.Image, dx, dy float64) image.Image {
	b := img.Bounds()
	ix, iy := int(float64(b.Dx())*dx), int(float64(b.Dy())*dy)
	src := image.Rect(b.Min.X+ix, b.Min.Y+iy, b.Max.X-ix, b.Max.Y-iy)
	if src.Dx() < 8 || src.Dy() < 8 {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, img.At(
				src.Min.X+x*src.Dx()/b.Dx(),
				src.Min.Y+y*src.Dy()/b.Dy()))
		}
	}
	return dst
}

// family is one augmentation strategy: a name and the variants it produces
// from a reference image. The identity variant is always included, so a family
// can only ever help — the minimum is taken across all of them.
type family struct {
	name    string
	variant []func(image.Image) image.Image
}

func families() []family {
	id := func(i image.Image) image.Image { return i }
	return []family{
		{"none (baseline)", []func(image.Image) image.Image{id}},
		{"blur", []func(image.Image) image.Image{id,
			func(i image.Image) image.Image { return blur(i, 1) },
			func(i image.Image) image.Image { return blur(i, 2) }}},
		{"glare (non-linear)", []func(image.Image) image.Image{id,
			func(i image.Image) image.Image { return glare(i, 0.35) },
			func(i image.Image) image.Image { return glare(i, 0.65) }}},
		{"warm/cool cast", []func(image.Image) image.Image{id,
			func(i image.Image) image.Image { return cast(i, 1.12, 1.0, 0.88) },
			func(i image.Image) image.Image { return cast(i, 0.90, 1.0, 1.12) }}},
		{"framing jitter", []func(image.Image) image.Image{id,
			func(i image.Image) image.Image { return jitter(i, 0.02, 0.02) },
			func(i image.Image) image.Image { return jitter(i, 0.04, 0.04) },
			func(i image.Image) image.Image { return jitter(i, 0.00, 0.03) },
			func(i image.Image) image.Image { return jitter(i, 0.03, 0.00) }}},
		{"all combined", []func(image.Image) image.Image{id,
			func(i image.Image) image.Image { return blur(i, 1) },
			func(i image.Image) image.Image { return glare(i, 0.35) },
			func(i image.Image) image.Image { return cast(i, 1.12, 1.0, 0.88) },
			func(i image.Image) image.Image { return jitter(i, 0.02, 0.02) },
			func(i image.Image) image.Image { return jitter(i, 0.04, 0.04) },
			func(i image.Image) image.Image { return jitter(i, 0.00, 0.03) },
			func(i image.Image) image.Image { return jitter(i, 0.03, 0.00) },
			func(i image.Image) image.Image { return blur(jitter(i, 0.03, 0.03), 1) }}},
	}
}

// ---- The measurement ----

func augSlug(s string) string {
	s = strings.ToLower(s)
	out := []rune{}
	for _, c := range s {
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
		case len(out) > 0 && out[len(out)-1] != '-':
			out = append(out, '-')
		}
	}
	return strings.Trim(string(out), "-")
}

func augFetch(u string) (image.Image, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "hoard/augment-eval")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	img, _, err := image.Decode(resp.Body)
	return img, err
}

func TestAugmentationSweep(t *testing.T) {
	if os.Getenv("HOARD_AUGMENT") == "" {
		t.Skip("set HOARD_AUGMENT=1 (and HOARD_CROPS=<dir of probe crops>) to sweep reference augmentations")
	}
	cropDir := os.Getenv("HOARD_CROPS")
	if cropDir == "" {
		t.Fatal("HOARD_CROPS must point at a directory of <still-id>.png probe crops")
	}
	repo := os.Getenv("HOARD_REPO")
	if repo == "" {
		repo = "../.."
	}

	// Ground truth for the s9 session.
	f, err := os.Open(repo + "/scan/foil-corpus/stills-labels.tsv")
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	recs, _ := r.ReadAll()
	f.Close()
	col := map[string]int{}
	for i, h := range recs[0] {
		col[h] = i
	}
	// Which capture session to score. The inset being fitted is a property of
	// how the probe frames its flatten, so a constant fitted on one session
	// and one rig has to be checked against the others before it is believed.
	session := os.Getenv("HOARD_SESSION")
	if session == "" {
		session = "s9"
	}
	want := map[string]string{}
	for _, rec := range recs[1:] {
		if id := rec[col["id"]]; strings.HasPrefix(id, session+"-") {
			want[id] = rec[col["physical"]]
		}
	}

	// The captures, hashed once — the query side never changes.
	capt := map[string]Hash{}
	var ids []string
	for id := range want {
		cf, err := os.Open(cropDir + "/" + id + ".png")
		if err != nil {
			continue
		}
		img, _, err := image.Decode(cf)
		cf.Close()
		if err != nil {
			continue
		}
		capt[id] = FromCard(img)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		t.Fatalf("no crops found in %s", cropDir)
	}

	// The reference images, fetched once and kept decoded so every family
	// hashes the same pixels.
	cacheRoot, _ := os.UserCacheDir()
	cat, err := catalog.Open(cacheRoot + "/hoard/catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	srcs, err := cat.ImageSources()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, w := range want {
		names[w] = true
	}
	refs := map[string][]image.Image{} // card slug -> its printings' images
	for _, s := range srcs {
		m, _ := cat.Cards([]string{s.ScryfallID})
		slug := augSlug(m[s.ScryfallID].Name)
		if !names[slug] {
			continue
		}
		img, err := augFetch(s.ImageURI)
		if err != nil {
			continue
		}
		refs[slug] = append(refs[slug], img)
		time.Sleep(80 * time.Millisecond)
	}
	total := 0
	for _, v := range refs {
		total += len(v)
	}
	t.Logf("%d captures, %d reference images across %d cards", len(ids), total, len(refs))

	fmt.Printf("\n%-18s %6s %8s %8s %8s %8s %9s\n",
		"family", "×refs", "own max", "oth min", "overlap", "own med", "correct")
	for _, fam := range families() {
		// Hash every reference under every variant in this family.
		hashes := map[string][]Hash{}
		for slug, imgs := range refs {
			for _, img := range imgs {
				for _, v := range fam.variant {
					hashes[slug] = append(hashes[slug], FromCard(v(img)))
				}
			}
		}
		var own, other []int
		correct := 0
		for _, id := range ids {
			bestOwn, bestOther := 1<<30, 1<<30
			nearest, nearestD := "", 1<<30
			for slug, hs := range hashes {
				b := 1 << 30
				for _, h := range hs {
					if d := capt[id].Distance(h); d < b {
						b = d
					}
				}
				if b < nearestD {
					nearestD, nearest = b, slug
				}
				if slug == want[id] {
					bestOwn = b
				} else if b < bestOther {
					bestOther = b
				}
			}
			own = append(own, bestOwn)
			other = append(other, bestOther)
			if nearest == want[id] {
				correct++
			}
		}
		sort.Ints(own)
		sort.Ints(other)
		ownMax, othMin := own[len(own)-1], other[0]
		fmt.Printf("%-18s %6d %8d %8d %8d %8d %6d/%d\n",
			fam.name, len(fam.variant), ownMax, othMin, ownMax-othMin,
			own[len(own)/2], correct, len(ids))
	}
	fmt.Printf("\noverlap = own-max minus other-min; ≤0 means the two distributions separate.\n")
	fmt.Printf("in-domain (capture vs capture) separates at -36 for comparison.\n")

	// Same jitter, applied to the QUERY instead of the reference.
	//
	// If the two are equivalent it changes everything about the cost. Jittering
	// references multiplies the index — 5x the rows or 5x the hashes per row,
	// across 107k printings, rebuilt whenever the variant set changes.
	// Jittering the query costs 5 hashes of one image at scan time and leaves
	// the index exactly as it is: one row per printing, no rebuild, no growth.
	//
	// Symmetry is plausible — both explore the same relative-framing space —
	// but plausible is not measured, so it is measured.
	fmt.Printf("\n-- the same jitter applied to the QUERY, against unaugmented references --\n")
	fmt.Printf("%-18s %6s %8s %8s %8s %8s %9s\n",
		"", "×query", "own max", "oth min", "overlap", "own med", "correct")

	base := map[string][]Hash{}
	for slug, imgs := range refs {
		for _, img := range imgs {
			base[slug] = append(base[slug], FromCard(img))
		}
	}
	queryVariants := []func(image.Image) image.Image{
		func(i image.Image) image.Image { return i },
		func(i image.Image) image.Image { return jitter(i, 0.02, 0.02) },
		func(i image.Image) image.Image { return jitter(i, 0.04, 0.04) },
		func(i image.Image) image.Image { return jitter(i, 0.00, 0.03) },
		func(i image.Image) image.Image { return jitter(i, 0.03, 0.00) },
	}
	var own, other []int
	correct := 0
	for _, id := range ids {
		cf, err := os.Open(cropDir + "/" + id + ".png")
		if err != nil {
			continue
		}
		img, _, err := image.Decode(cf)
		cf.Close()
		if err != nil {
			continue
		}
		var qh []Hash
		for _, v := range queryVariants {
			qh = append(qh, FromCard(v(img)))
		}
		bestOwn, bestOther := 1<<30, 1<<30
		nearest, nearestD := "", 1<<30
		for slug, hs := range base {
			b := 1 << 30
			for _, h := range hs {
				for _, q := range qh {
					if d := q.Distance(h); d < b {
						b = d
					}
				}
			}
			if b < nearestD {
				nearestD, nearest = b, slug
			}
			if slug == want[id] {
				bestOwn = b
			} else if b < bestOther {
				bestOther = b
			}
		}
		own = append(own, bestOwn)
		other = append(other, bestOther)
		if nearest == want[id] {
			correct++
		}
	}
	sort.Ints(own)
	sort.Ints(other)
	fmt.Printf("%-18s %6d %8d %8d %8d %8d %6d/%d\n",
		"query-side jitter", len(queryVariants),
		own[len(own)-1], other[0], own[len(own)-1]-other[0],
		own[len(own)/2], correct, len(ids))

	// If the framing mismatch is SYSTEMATIC rather than random — the probe's
	// flatten consistently crops into the card, by roughly the same amount
	// every time — then one fixed inset captures the gain and the variant set
	// is unnecessary. That is the difference between a 5x index and a
	// calibration constant, so it is worth one more sweep.
	fmt.Printf("\n-- ONE fixed inset on the reference, no variants, no identity --\n")
	fmt.Printf("%-18s %6s %8s %8s %8s %8s %9s\n",
		"inset", "×refs", "own max", "oth min", "overlap", "own med", "correct")
	for _, in := range []float64{0.00, 0.01, 0.02, 0.03, 0.04, 0.05, 0.06} {
		hashes := map[string][]Hash{}
		for slug, imgs := range refs {
			for _, img := range imgs {
				hashes[slug] = append(hashes[slug], FromCard(jitter(img, in, in)))
			}
		}
		var own, other []int
		correct := 0
		for _, id := range ids {
			bestOwn, bestOther := 1<<30, 1<<30
			nearest, nearestD := "", 1<<30
			for slug, hs := range hashes {
				b := 1 << 30
				for _, h := range hs {
					if d := capt[id].Distance(h); d < b {
						b = d
					}
				}
				if b < nearestD {
					nearestD, nearest = b, slug
				}
				if slug == want[id] {
					bestOwn = b
				} else if b < bestOther {
					bestOther = b
				}
			}
			own = append(own, bestOwn)
			other = append(other, bestOther)
			if nearest == want[id] {
				correct++
			}
		}
		sort.Ints(own)
		sort.Ints(other)
		fmt.Printf("%-18.2f %6d %8d %8d %8d %8d %6d/%d\n",
			in, 1, own[len(own)-1], other[0], own[len(own)-1]-other[0],
			own[len(own)/2], correct, len(ids))
	}
}

// popcountsAreSane is a cheap guard on the augmentation helpers: a variant
// that returned a blank image would hash to a constant and quietly look like a
// brilliant improvement, because a constant sits far from everything.
func TestAugmentationsPreserveStructure(t *testing.T) {
	base := synthImage(11, 146, 204)
	h := FromCard(base)
	for _, tc := range []struct {
		name string
		img  image.Image
		max  int
	}{
		{"blur r1", blur(base, 1), 60},
		{"glare 0.35", glare(base, 0.35), 60},
		{"warm cast", cast(base, 1.12, 1.0, 0.88), 60},
		{"jitter 2%", jitter(base, 0.02, 0.02), 90},
	} {
		d := h.Distance(FromCard(tc.img))
		if d == 0 {
			t.Errorf("%s: identical to the source — the augmentation did nothing", tc.name)
		}
		if d > tc.max {
			t.Errorf("%s: moved the hash %d bits, want ≤%d — it is destroying the picture, "+
				"not degrading it", tc.name, d, tc.max)
		}
	}
}

// The hash cannot see a linear exposure or contrast change. Worth pinning
// down, because it retires a standing theory: "foil glare washes the art out
// and eats the margins" cannot be the mechanism unless the washing is
// non-linear. Every kept DCT coefficient scales by the same factor, so each
// one's position relative to their median — which is what the bits record —
// does not move, and the DC term (overall brightness) is dropped outright.
//
// The invariance is exact up to quantisation, and both edges were measured:
// k=1.2 expands and CLIPS at the ends of the range, moving the hash 6 bits;
// k=0.3 compresses into so narrow a band that integer rounding alone moves it
// 2. Clipping and rounding are the only ways a "linear" change stops being one.
func TestHashIgnoresLinearExposureAndContrast(t *testing.T) {
	base := synthImage(11, 146, 204)
	h := FromCard(base)
	for _, k := range []float64{0.95, 0.9, 0.75, 0.55} {
		if d := h.Distance(FromCard(flatten(base, k))); d != 0 {
			t.Errorf("linear contrast k=%.2f moved the hash %d bits, want 0", k, d)
		}
	}
}
