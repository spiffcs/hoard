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
)

// Hash is a 64-bit DCT perceptual hash. Two images of the same printing land
// within a few bits of each other across JPEG recompression, mild blur and
// exposure shifts; different printings land tens of bits apart.
type Hash uint64

// Distance is the Hamming distance between two hashes.
func (h Hash) Distance(o Hash) int { return bits.OnesCount64(uint64(h ^ o)) }

// phashSize is the working grid: the image is reduced to 32x32 grayscale, a
// 2D DCT is taken, and the hash is the sign of the lowest 8x8 frequencies
// against their median. The standard construction — chosen over average-hash
// because the DCT's low frequencies survive the exact distortions a phone
// capture adds (lighting gradients, slight blur), which an intensity mean
// does not.
const (
	phashSize = 32
	phashKeep = 8
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

	// The 8x8 low-frequency block, skipping the DC term — DC is overall
	// brightness, the one thing two photographs of one card never share.
	var vals []float64
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
				h |= 1 << bit
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

// dct2d is the type-II DCT of a 32x32 block, direct evaluation. ~130k
// multiplies with the cosine table hoisted — microseconds per image, and the
// index build is network-bound regardless.
func dct2d(px [phashSize][phashSize]float64) [phashSize][phashSize]float64 {
	var cos [phashSize][phashSize]float64
	for k := 0; k < phashSize; k++ {
		for n := 0; n < phashSize; n++ {
			cos[k][n] = math.Cos(math.Pi / phashSize * (float64(n) + 0.5) * float64(k))
		}
	}
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

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	for i := 1; i < len(s); i++ { // insertion sort: n=63, once per image
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s[len(s)/2]
}
