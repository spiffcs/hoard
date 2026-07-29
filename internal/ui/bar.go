package ui

import (
	"math"
	"strings"
)

// blocks are the eighth-width block glyphs, 1/8 through 8/8. They are what
// makes a share-of-total bar viable: with 22 decks no single deck exceeds ~5%
// of the hoard, so without sub-cell resolution every deck row would round to
// nothing and the column would carry no information at all.
var blocks = []rune("▏▎▍▌▋▊▉█")

// Bar renders frac (0…1) as a proportional bar at most cells columns wide.
//
// Eighths are floored rather than rounded, so a share never renders larger
// than it is. Exactly zero renders empty — which makes a blank bar mean
// "$0.00", i.e. a deck whose prices were never fetched — while any nonzero
// share renders at least the narrowest glyph.
func Bar(frac float64, cells int) string {
	if cells <= 0 || math.IsNaN(frac) || frac <= 0 {
		return ""
	}
	if frac > 1 {
		frac = 1
	}

	eighths := max(int(frac*float64(cells)*8), 1)

	full, rem := eighths/8, eighths%8
	if full >= cells {
		return strings.Repeat(string(blocks[7]), cells)
	}

	var b strings.Builder
	b.WriteString(strings.Repeat(string(blocks[7]), full))
	if rem > 0 {
		b.WriteRune(blocks[rem-1])
	}
	return b.String()
}
