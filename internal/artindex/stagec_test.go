package artindex

// Stage C, step 1: the footprint x bits go/no-go, measured on BOTH hash widths
// over identical bytes.
//
// The question this answers is not "are the 256-bit margins bigger" — they are
// arithmetically bound to be, four times as many bits. It is whether the wider
// hash buys a gate that accepts more correct reads at zero wrong ones. So both
// footprints are built from the same fetched images and scored against the same
// crops, and each is given its own best possible gate by exhaustive sweep.
//
// Internal test (package artindex, not artindex_test) because reproducing the
// old 8x8 keep block needs grayGrid, dct2d and median directly.
//
// Gated: HOARD_STAGEC=1. Fetches ~536 images from Scryfall at 80ms spacing.

import (
	"encoding/csv"
	"fmt"
	"image"
	_ "image/png"
	"math/bits"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/catalog"
)

// h64 reproduces the pre-2026-08-08 hash exactly: same 32x32 grid, same median
// rule, 8x8 keep block instead of 16x16.
type h64 uint64

func (a h64) dist(b h64) int { return bits.OnesCount64(uint64(a ^ b)) }

func fromCard64(img image.Image) h64 {
	b := img.Bounds()
	crop := image.Rect(
		b.Min.X+b.Dx()*8/100, b.Min.Y+b.Dy()*10/100,
		b.Min.X+b.Dx()*92/100, b.Min.Y+b.Dy()*58/100)
	d := dct2d(grayGrid(&cropped{img, crop}))
	var vals []float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if x == 0 && y == 0 {
				continue
			}
			vals = append(vals, d[y][x])
		}
	}
	med := median(vals)
	var h h64
	bit := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if x == 0 && y == 0 {
				continue
			}
			if d[y][x] > med {
				h |= 1 << bit
			}
			bit++
		}
	}
	return h
}

var stageCNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func stageCSlug(s string) string {
	return strings.Trim(stageCNonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// read is one still's outcome under one footprint.
type read struct {
	still  string
	best   int
	margin int
	ok     bool // the winner is the printing the label says is on the desk
}

// bestGate finds the (maxDistance, minMargin) pair accepting the most reads
// with ZERO wrong ones — the sprint's hard bar. Exhaustive, because the space
// is 257x257 and the honest answer beats a clever one.
func bestGate(reads []read, width int) (maxD, minM, decisive int) {
	for d := 0; d <= width; d += 2 {
		for m := 0; m <= width; m += 2 {
			acc, wrong := 0, 0
			for _, r := range reads {
				if r.best <= d && r.margin >= m {
					acc++
					if !r.ok {
						wrong++
					}
				}
			}
			if wrong == 0 && acc > decisive {
				maxD, minM, decisive = d, m, acc
			}
		}
	}
	return maxD, minM, decisive
}

func TestStageCFootprintComparison(t *testing.T) {
	if os.Getenv("HOARD_STAGEC") == "" {
		t.Skip("set HOARD_STAGEC=1 to run the Stage C footprint comparison")
	}
	repo := os.Getenv("HOARD_REPO")
	if repo == "" {
		repo = "../.."
	}
	cacheRoot, _ := os.UserCacheDir()
	cat, err := catalog.Open(filepath.Join(cacheRoot, "hoard", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	srcs, err := cat.ImageSources()
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		name string
		h256 Hash
		h64  h64
	}
	var idx []row
	client := &http.Client{Timeout: 20 * time.Second}
	// Both hashes from one fetch: the comparison is only meaningful if the two
	// footprints see identical bytes.
	fetch := func(url, name string) bool {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "hoard/stage-c-eval")
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		img, _, err := image.Decode(resp.Body)
		if err != nil {
			return false
		}
		idx = append(idx, row{name, FromCard(img), fromCard64(img)})
		return true
	}

	added := 0
	for i, s := range srcs {
		if i%200 != 0 || added >= 520 {
			continue
		}
		m, _ := cat.Cards([]string{s.ScryfallID})
		if fetch(s.ImageURI, m[s.ScryfallID].Name) {
			added++
			time.Sleep(80 * time.Millisecond)
		}
	}
	wantPrints := map[string][2]string{
		"brainsurge": {"mh3", "399"}, "dress-down": {"h2r", "4"}, "meltdown": {"mh3", "418"},
		"victimize": {"mh3", "413"}, "consuming-corruption": {"mh3", "407"},
		"lion-umbra": {"mh3", "426"}, "ornithopter": {"m15", "223"},
		"unstable-amulet": {"mh3", "421"}, "charitable-levy": {"mh3", "390"},
		"unholy-heat": {"h2r", "13"}, "glowrider": {"lgn", "15"},
		"abiding-grace": {"h2r", "1"}, "primal-prayers": {"mh3", "429"},
		"hollow-specter": {"lgn", "75"}, "trap-digger": {"scg", "24"},
		"hard-evidence": {"h2r", "5"},
	}
	for slug, sn := range wantPrints {
		card, err := cat.PrintBySetNumber(t.Context(), sn[0], sn[1])
		if err != nil || card == nil || card.ImageURI == "" {
			t.Logf("no catalog row for %s (%s/%s): %v", slug, sn[0], sn[1], err)
			continue
		}
		if fetch(card.ImageURI, card.Name) {
			time.Sleep(80 * time.Millisecond)
		}
	}
	t.Logf("index: %d entries, both footprints", len(idx))

	// Ground truth: what was physically on the desk for each still.
	want := map[string]string{}
	f, err := os.Open(filepath.Join(repo, "scan", "foil-corpus", "stills-labels.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	recs, _ := r.ReadAll()
	f.Close()
	head := recs[0]
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	for _, rec := range recs[1:] {
		want[rec[col["id"]]] = rec[col["physical"]]
	}

	probe := filepath.Join(repo, "bin", "cardkit-probe")
	crop := filepath.Join(os.TempDir(), "stagec-crop.png")
	stills, _ := filepath.Glob(filepath.Join(repo, "scan", "foil-corpus", "stills", "s9-*.jpg"))
	var r256, r64 []read
	fmt.Printf("%-10s %-24s %-28s %-28s\n", "still", "want", "256-bit", "64-bit")
	for _, still := range stills {
		id := strings.TrimSuffix(filepath.Base(still), ".jpg")
		os.Remove(crop)
		exec.Command(probe, "--image", still, "--emit-card", crop).Run()
		cf, err := os.Open(crop)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(cf)
		cf.Close()
		if err != nil {
			continue
		}
		q256, q64 := FromCard(img), fromCard64(img)

		b1, b2, n1 := maxDistance+1, maxDistance+1, ""
		c1, c2, m1 := 65, 65, ""
		for _, e := range idx {
			if d := q256.Distance(e.h256); d < b1 {
				b2, b1, n1 = b1, d, e.name
			} else if d < b2 {
				b2 = d
			}
			if d := q64.dist(e.h64); d < c1 {
				c2, c1, m1 = c1, d, e.name
			} else if d < c2 {
				c2 = d
			}
		}
		ok256 := stageCSlug(n1) == want[id]
		ok64 := stageCSlug(m1) == want[id]
		r256 = append(r256, read{id, b1, b2 - b1, ok256})
		r64 = append(r64, read{id, c1, c2 - c1, ok64})
		mark := func(ok bool) string {
			if ok {
				return "OK "
			}
			return "!! "
		}
		fmt.Printf("%-10s %-24s %sd=%3d m=%3d %-8s %sd=%2d m=%2d %s\n",
			id, want[id],
			mark(ok256), b1, b2-b1, trunc(n1),
			mark(ok64), c1, c2-c1, trunc(m1))
	}

	report := func(label string, reads []read, width int) {
		correct := 0
		for _, r := range reads {
			if r.ok {
				correct++
			}
		}
		d, m, dec := bestGate(reads, width)
		fmt.Printf("\n%s: %d/%d correct nearest-neighbour (%.0f%%)\n",
			label, correct, len(reads), 100*float64(correct)/float64(len(reads)))
		fmt.Printf("%s: best zero-wrong gate = distance ≤%d, margin ≥%d "+
			"→ %d/%d decisive (%.0f%%)\n",
			label, d, m, dec, len(reads), 100*float64(dec)/float64(len(reads)))
	}
	report("256-bit", r256, maxDistance)
	report(" 64-bit", r64, 64)
}

func trunc(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
