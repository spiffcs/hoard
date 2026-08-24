package store

import "testing"

func points(prices ...float64) []PricePoint {
	out := make([]PricePoint, len(prices))
	for i, p := range prices {
		out[i] = PricePoint{AsOf: "2026-01-01", Price: p}
	}
	return out
}

func TestStreakCountsTheRunAtTheEndOfTheSeries(t *testing.T) {
	for _, c := range []struct {
		name   string
		prices []float64
		want   int
	}{
		{"every check up", []float64{1, 2, 3, 4, 5}, 4},
		{"every check down", []float64{5, 4, 3}, -2},
		{"only the tail counts", []float64{1, 9, 8, 7, 6}, -3},
		{"a rise after a fall", []float64{9, 1, 2, 3, 4}, 3},
		{"a flat check neither breaks nor counts", []float64{1, 2, 3, 3, 4}, 3},
		{"a flat tail leaves the run standing", []float64{1, 2, 3, 4, 4}, 3},
		{"a reversal ends the run", []float64{1, 2, 3, 4, 3}, -1},
		{"nothing ever moved", []float64{3, 3, 3}, 0},
		{"a single check", []float64{3}, 0},
		{"no checks", nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Streak(points(c.prices...)); got != c.want {
				t.Errorf("Streak(%v) = %d, want %d", c.prices, got, c.want)
			}
		})
	}
}
