package ui

import (
	"math"
	"strings"
)

// sparks are the eighth-height block glyphs, 1/8 through 8/8. Unlike the
// left-to-right blocks Bar uses, these grow upward, so a row of them reads as a
// line chart rather than as a set of bars.
var sparks = []rune("▁▂▃▄▅▆▇█")

// Spark renders a series as a one-line chart at most cells wide.
//
// Scaled to the series' own range, not to zero: a card drifting between $34.00 and
// $34.50 would otherwise look identical to one that never moved. The cost is that
// two sparklines are not comparable — the numbers beside them carry magnitude.
//
// A flat series renders at mid height, so "never moved" and "bottomed out" differ.
//
// Longer series are averaged into buckets by index, so callers with irregularly
// timed observations should resample onto an even time base first.
func Spark(values []float64, cells int) string {
	if cells <= 0 {
		return ""
	}
	values = finite(values)
	if len(values) == 0 {
		return ""
	}
	if len(values) > cells {
		values = downsample(values, cells)
	}

	lo, hi := values[0], values[0]
	for _, v := range values {
		lo = min(lo, v)
		hi = max(hi, v)
	}

	var b strings.Builder
	span := hi - lo
	for _, v := range values {
		if span == 0 {
			b.WriteRune(sparks[len(sparks)/2-1])
			continue
		}
		// Scaled across the glyphs and clamped, so floating-point drift at the
		// top of the range cannot index past the last rune.
		i := int((v - lo) / span * float64(len(sparks)-1))
		b.WriteRune(sparks[min(max(i, 0), len(sparks)-1)])
	}
	return b.String()
}

// finite drops NaN and infinity, which would otherwise poison the whole range
// and flatten every other point to the floor.
func finite(values []float64) []float64 {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		}
	}
	return out
}

// downsample averages values into exactly cells buckets, preserving order.
func downsample(values []float64, cells int) []float64 {
	out := make([]float64, cells)
	for i := range out {
		// Proportional bucket bounds, so the remainder is spread across the
		// series rather than piled into the last bucket.
		start := i * len(values) / cells
		end := (i + 1) * len(values) / cells
		if end <= start {
			end = start + 1
		}
		var sum float64
		for _, v := range values[start:end] {
			sum += v
		}
		out[i] = sum / float64(end-start)
	}
	return out
}
