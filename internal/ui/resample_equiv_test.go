package ui

import (
	"fmt"
	"testing"
	"time"
)

// resampleReference is the pre-optimisation implementation, kept verbatim so the
// faster one can be held to it.
func resampleReference(points []TimePoint, buckets int) []float64 {
	if len(points) == 0 || buckets <= 0 {
		return nil
	}
	if len(points) == 1 {
		return []float64{points[0].Value}
	}
	first, err := time.Parse(time.RFC3339, points[0].AsOf)
	if err != nil {
		return nil
	}
	last, err := time.Parse(time.RFC3339, points[len(points)-1].AsOf)
	if err != nil {
		return nil
	}
	span := last.Sub(first)
	if span <= 0 {
		return []float64{points[len(points)-1].Value}
	}
	out := make([]float64, 0, buckets)
	at := 0
	for i := range buckets {
		edge := first.Add(time.Duration(float64(span) * float64(i+1) / float64(buckets)))
		for at+1 < len(points) {
			t, err := time.Parse(time.RFC3339, points[at+1].AsOf)
			if err != nil || t.After(edge) {
				break
			}
			at++
		}
		out = append(out, points[at].Value)
	}
	return out
}

func series(n int, gapHours func(i int) int, corrupt map[int]bool) []TimePoint {
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	out := make([]TimePoint, n)
	at := base
	for i := range n {
		out[i] = TimePoint{AsOf: at.Format(time.RFC3339), Value: float64(i*7%23) + 0.5}
		if corrupt[i] {
			out[i].AsOf = "not-a-timestamp"
		}
		at = at.Add(time.Duration(gapHours(i)) * time.Hour)
	}
	return out
}

func TestResampleMatchesTheReference(t *testing.T) {
	cases := []struct {
		name    string
		points  []TimePoint
		corrupt map[int]bool
	}{
		{name: "empty"},
		{name: "one", points: series(1, func(int) int { return 24 }, nil)},
		{name: "even 30d", points: series(30, func(int) int { return 24 }, nil)},
		{name: "even 90d", points: series(90, func(int) int { return 24 }, nil)},
		{name: "irregular", points: series(60, func(i int) int { return 1 + i%53 }, nil)},
		{name: "all same instant", points: series(20, func(int) int { return 0 }, nil)},
		{name: "clustered then gap", points: series(40, func(i int) int {
			if i < 35 {
				return 1
			}
			return 500
		}, nil)},
		{name: "corrupt middle", points: series(30, func(int) int { return 24 },
			map[int]bool{11: true})},
		{name: "corrupt first", points: series(30, func(int) int { return 24 },
			map[int]bool{0: true})},
		{name: "corrupt last", points: series(30, func(int) int { return 24 },
			map[int]bool{29: true})},
	}

	for _, c := range cases {
		for _, buckets := range []int{0, 1, 2, 8, 32, 64, 200} {
			got := Resample(c.points, buckets)
			want := resampleReference(c.points, buckets)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s buckets=%d:\n got %v\nwant %v", c.name, buckets, got, want)
			}
		}
	}
}

func BenchmarkResample(b *testing.B) {
	points := series(90, func(int) int { return 24 }, nil)
	b.ResetTimer()
	for b.Loop() {
		_ = Resample(points, 32)
	}
}
