package ui

import (
	"testing"
	"time"
)

func TestResample(t *testing.T) {
	pt := func(day int, v float64) TimePoint {
		return TimePoint{
			AsOf:  time.Date(2026, 5, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Value: v,
		}
	}
	// A value that moved on day 1 then held for a month, then moved again. By
	// index this is three equal thirds; by time the flat month dominates, which
	// is the truth a sparkline should show.
	series := []TimePoint{pt(1, 10), pt(2, 20), pt(31, 30)}
	got := Resample(series, 10)
	if len(got) != 10 {
		t.Fatalf("got %d buckets, want 10", len(got))
	}
	if got[len(got)-1] != 30 {
		t.Errorf("last bucket = %v, want the latest value", got[len(got)-1])
	}
	var at20 int
	for _, v := range got {
		if v == 20 {
			at20++
		}
	}
	if at20 < 6 {
		t.Errorf("only %d of 10 buckets sit at the month-long value: %v", at20, got)
	}
}

func TestResampleEdgeCases(t *testing.T) {
	if got := Resample(nil, 8); got != nil {
		t.Errorf("nil series = %v, want nil", got)
	}
	one := []TimePoint{{AsOf: "2026-05-01T00:00:00Z", Value: 7}}
	if got := Resample(one, 8); len(got) != 1 || got[0] != 7 {
		t.Errorf("single point = %v, want [7]", got)
	}
	// Two observations at the same instant have no span to spread across.
	same := []TimePoint{
		{AsOf: "2026-05-01T00:00:00Z", Value: 7},
		{AsOf: "2026-05-01T00:00:00Z", Value: 9},
	}
	if got := Resample(same, 8); len(got) != 1 || got[0] != 9 {
		t.Errorf("zero-span series = %v, want the latest value", got)
	}
}
