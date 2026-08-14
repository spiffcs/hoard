package ui

import (
	"math"
	"strings"
)

var sparks = []rune("▁▂▃▄▅▆▇█")

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

		i := int((v - lo) / span * float64(len(sparks)-1))
		b.WriteRune(sparks[min(max(i, 0), len(sparks)-1)])
	}
	return b.String()
}

func finite(values []float64) []float64 {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		}
	}
	return out
}

func downsample(values []float64, cells int) []float64 {
	out := make([]float64, cells)
	for i := range out {

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
