// Package artindex identifies a card printing by its picture rather than its
// text: a perceptual hash of every printing's Scryfall image, matched by
// Hamming distance against a hash of the scanned card. OCR never sees the
// art, so glare on the copyright line — the scanner's dominant live failure —
// does not touch this channel, and same-name printings differ in art or
// frame, which is exactly what a text rank cannot separate.
package artindex

import (
	"image"
	"image/color"
	"math"
	"math/bits"
	"sort"
)

// Hash is a 256-bit DCT perceptual hash. Two images of the same printing land
// within a few bits of each other across JPEG recompression, mild blur and
// exposure shifts; different printings land tens of bits apart.
//
// Four words rather than one, since 2026-08-08. The 64-bit version measured
// 31/35 correct nearest-neighbour on hand-held stills but with margins of only
// 2-6 bits between the winner and the runner-up — too thin to gate on, because
// foil glare compresses exactly those low frequencies. More bits is the direct
// answer: the same distortion moves a proportional number of bits either way,
// so a wider hash separates the same pair by a wider absolute margin.
type Hash [hashWords]uint64

// Distance is the Hamming distance between two hashes.
func (h Hash) Distance(o Hash) int {
	d := 0
	for i := range h {
		d += bits.OnesCount64(h[i] ^ o[i])
	}
	return d
}

// phashSize is the working grid: the image is reduced to 32x32 grayscale, a
// 2D DCT is taken, and the hash is the sign of the lowest 16x16 frequencies
// against their median. The standard construction — chosen over average-hash
// because the DCT's low frequencies survive the exact distortions a phone
// capture adds (lighting gradients, slight blur), which an intensity mean
// does not.
//
// The grid stays 32x32 while the keep block grows to 16x16. That pairing is
// deliberate: the DCT of a 32x32 block has 32x32 coefficients, so a 16x16 keep
// is still the lower half of the spectrum in each axis — real image structure,
// not the sampling noise the top octave carries.
const (
	phashSize = 32
	phashKeep = 16
	// hashBits is the keep block minus the DC term, which is dropped because
	// it is overall brightness — the one thing two photographs of one card
	// never share. 255 bits in 4 words leaves the top bit of the last word
	// always zero; that costs nothing and keeps the layout arithmetic plain.
	hashBits  = phashKeep*phashKeep - 1
	hashWords = 4
	// maxDistance is every bit differing. Callers seed their "nothing found
	// yet" sentinel above this.
	maxDistance = hashWords * 64
)

// FromCard hashes a full-card image by its central region — the art and the
// upper text, u 0.08-0.92 × v 0.10-0.58 of the card.
//
// Not the whole card, and the crop is the finding: whole-card hashes
// saturate, because every card shares its global structure — border, art
// box, text box, all at the same positions — so the low-frequency DCT signs
// mostly agree across the entire catalog and 124 labelled captures matched
// at distances 4-16 with margins of 0-2, right or wrong alike (offline eval,
// 2026-08-07). The art region is where printings actually differ. Both sides
// of a comparison must use this same footprint: the index build and the
// scanner's flatten both come through here.
func FromCard(img image.Image) Hash {
	b := img.Bounds()
	crop := image.Rect(
		b.Min.X+b.Dx()*8/100, b.Min.Y+b.Dy()*10/100,
		b.Min.X+b.Dx()*92/100, b.Min.Y+b.Dy()*58/100)
	return FromImage(&cropped{img, crop})
}

// cropped is a zero-copy sub-image view; image.Image only, no SubImage
// interface needed on the source.
type cropped struct {
	src image.Image
	r   image.Rectangle
}

func (c *cropped) ColorModel() color.Model { return c.src.ColorModel() }
func (c *cropped) Bounds() image.Rectangle { return c.r }
func (c *cropped) At(x, y int) color.Color { return c.src.At(x, y) }

// FromImage hashes an image over its full bounds. Prefer FromCard for
// anything card-shaped — see its comment for why.
func FromImage(img image.Image) Hash {
	px := grayGrid(img)
	d := dct2d(px)

	// The 16x16 low-frequency block, skipping the DC term — DC is overall
	// brightness, the one thing two photographs of one card never share.
	vals := make([]float64, 0, hashBits)
	for y := 0; y < phashKeep; y++ {
		for x := 0; x < phashKeep; x++ {
			if x == 0 && y == 0 {
				continue
			}
			vals = append(vals, d[y][x])
		}
	}
	med := median(vals)

	var h Hash
	bit := 0
	for y := 0; y < phashKeep; y++ {
		for x := 0; x < phashKeep; x++ {
			if x == 0 && y == 0 {
				continue
			}
			if d[y][x] > med {
				h[bit/64] |= 1 << (bit % 64)
			}
			bit++
		}
	}
	return h
}

// grayGrid reduces the image to the 32x32 luma working grid by area
// averaging: every source pixel contributes to exactly one cell. Box
// filtering, deliberately, over a fancier kernel — a dependency-free
// downscale whose worst artifact (mild aliasing) sits in frequencies the
// hash discards anyway.
func grayGrid(img image.Image) [phashSize][phashSize]float64 {
	b := img.Bounds()
	var sum, cnt [phashSize][phashSize]float64
	// A degenerate crop (<1px in either axis — a probe emitting a sliver, or
	// FromCard's percentage crop collapsing on a tiny source) has nothing to
	// average. The zero grid hashes to a constant, which sits far from every
	// real card and fails the caller's distance gates — better than trusting
	// grid arithmetic against an empty or inverted Bounds.
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return sum
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		gy := (y - b.Min.Y) * phashSize / b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			gx := (x - b.Min.X) * phashSize / b.Dx()
			r, g, bl, _ := img.At(x, y).RGBA()
			luma := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
			sum[gy][gx] += luma / 257
			cnt[gy][gx]++
		}
	}
	var px [phashSize][phashSize]float64
	for y := range px {
		for x := range px[y] {
			if cnt[y][x] > 0 {
				px[y][x] = sum[y][x] / cnt[y][x]
			}
		}
	}
	return px
}

// dctCos is the cosine table, built once for the process.
//
// It used to be rebuilt inside dct2d on every call — 1024 math.Cos evaluations
// per image — while the comment above dct2d claimed it was hoisted. That cost
// nothing worth measuring while the only caller was a network-bound build
// paced at ten images a second. `artindex rehash` changed that: it reads the
// image corpus off local disk, so the whole ~107k-printing pass is CPU-bound
// and this table would be rebuilt 107k times for no reason.
var dctCos = func() [phashSize][phashSize]float64 {
	var cos [phashSize][phashSize]float64
	for k := 0; k < phashSize; k++ {
		for n := 0; n < phashSize; n++ {
			cos[k][n] = math.Cos(math.Pi / phashSize * (float64(n) + 0.5) * float64(k))
		}
	}
	return cos
}()

// dct2d is the type-II DCT of a 32x32 block, direct evaluation. ~130k
// multiplies against the hoisted cosine table — microseconds per image.
func dct2d(px [phashSize][phashSize]float64) [phashSize][phashSize]float64 {
	cos := dctCos
	var tmp, out [phashSize][phashSize]float64
	for y := 0; y < phashSize; y++ { // rows
		for k := 0; k < phashSize; k++ {
			var s float64
			for n := 0; n < phashSize; n++ {
				s += px[y][n] * cos[k][n]
			}
			tmp[y][k] = s
		}
	}
	for x := 0; x < phashSize; x++ { // columns
		for k := 0; k < phashSize; k++ {
			var s float64
			for n := 0; n < phashSize; n++ {
				s += tmp[n][x] * cos[k][n]
			}
			out[k][x] = s
		}
	}
	return out
}

// median of the kept coefficients — the threshold every hash bit is taken
// against.
//
// sort.Float64s rather than the insertion sort this used to carry. That was
// justified at n=63; at n=255 it is 16x the comparisons, and `artindex rehash`
// runs it ~107k times back to back with no network in between to hide it.
func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}
