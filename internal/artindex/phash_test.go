package artindex

import (
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// A deterministic pseudo-card: structured enough that its hash is not
// degenerate, reproducible so distances are stable across runs.
func synthImage(seed int64, w, h int) image.Image {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for b := 0; b < 40; b++ { // blocks of tone, like art regions
		x0, y0 := rng.Intn(w), rng.Intn(h)
		x1, y1 := x0+rng.Intn(w/2)+1, y0+rng.Intn(h/2)+1
		c := color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
		for y := y0; y < y1 && y < h; y++ {
			for x := x0; x < x1 && x < w; x++ {
				img.Set(x, y, c)
			}
		}
	}
	return img
}

func TestHashIsStableAcrossScaleAndBrightness(t *testing.T) {
	base := synthImage(7, 146, 204) // Scryfall small-image footprint
	h := FromImage(base)
	if h.Distance(FromImage(base)) != 0 {
		t.Fatal("hashing is not deterministic")
	}

	// The same picture at capture resolution: a flattened card is much
	// larger than the indexed small image, and the hash must not care.
	big := image.NewRGBA(image.Rect(0, 0, 630, 880))
	for y := 0; y < 880; y++ {
		for x := 0; x < 630; x++ {
			big.Set(x, y, base.At(x*146/630, y*204/880))
		}
	}
	if d := h.Distance(FromImage(big)); d > 6 {
		t.Errorf("scale changed the hash by %d bits, want ≤6", d)
	}

	// A global brightness shift — the one thing two photographs never
	// share — must be nearly free: the DC term is excluded by design.
	bright := image.NewRGBA(base.Bounds())
	for y := 0; y < 204; y++ {
		for x := 0; x < 146; x++ {
			r, g, b, _ := base.At(x, y).RGBA()
			lift := func(v uint32) uint8 {
				n := v/257 + 40
				if n > 255 {
					n = 255
				}
				return uint8(n)
			}
			bright.Set(x, y, color.RGBA{lift(r), lift(g), lift(b), 255})
		}
	}
	if d := h.Distance(FromImage(bright)); d > 8 {
		t.Errorf("brightness shifted the hash by %d bits, want ≤8", d)
	}
}

func TestDifferentImagesLandFarApart(t *testing.T) {
	// 20 distinct pseudo-cards: every pair must sit further apart than any
	// plausible same-card acceptance bar. The margin between same-image
	// distortion (≤8 above) and cross-image distance here is what the
	// match rank's fail-closed thresholds get fitted inside.
	var hs []Hash
	for s := int64(0); s < 20; s++ {
		hs = append(hs, FromImage(synthImage(s, 146, 204)))
	}
	for i := range hs {
		for j := i + 1; j < len(hs); j++ {
			if d := hs[i].Distance(hs[j]); d < 14 {
				t.Errorf("images %d and %d only %d bits apart", i, j, d)
			}
		}
	}
}

func TestIndexRoundTripAndBest(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if _, err := ix.db.Exec(`INSERT INTO hashes VALUES ('a', ?, 'foil'), ('b', ?, '')`,
		int64(FromImage(synthImage(1, 146, 204))),
		int64(FromImage(synthImage(2, 146, 204)))); err != nil {
		t.Fatal(err)
	}
	if err := ix.reload(); err != nil {
		t.Fatal(err)
	}
	best, second := ix.Best(FromImage(synthImage(1, 146, 204)))
	if best.ScryfallID != "a" || best.Distance != 0 {
		t.Errorf("best = %+v, want a at 0", best)
	}
	if second.ScryfallID != "b" || second.Distance < 14 {
		t.Errorf("second = %+v, want b comfortably far", second)
	}
	if ix.SoleFinish("a") != "foil" || ix.SoleFinish("b") != "" {
		t.Error("sole finish did not round-trip")
	}
}

// A degenerate crop hashes to the zero grid instead of trusting grid math
// against an empty Bounds: the probe can emit a sliver, and FromCard's
// percentage crop collapses to nothing on a tiny source. The constant hash
// it produces sits far from every real card, so the caller's distance gates
// reject it.
func TestDegenerateCropsHashWithoutPanicking(t *testing.T) {
	_ = FromImage(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	_ = FromCard(image.NewRGBA(image.Rect(0, 0, 1, 1)))
}
