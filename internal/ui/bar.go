package ui

import (
	"math"
	"strings"
)

var blocks = []rune("▏▎▍▌▋▊▉█")

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
