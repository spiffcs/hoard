package store

import (
	"testing"
	"time"
)

func TestSettlingAtMatchesSettling(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	cutoff := SettlingCutoff(now)

	dates := []string{""}
	for d := -400; d <= 10; d += 7 {
		dates = append(dates, now.AddDate(0, 0, d).Format(time.DateOnly))
	}
	for _, days := range []int{0, 1, 14, DefaultSettlingDays, 90} {
		SetSettlingDays(days)
		cutoff = SettlingCutoff(now)
		for _, r := range dates {
			if got, want := SettlingAt(r, cutoff), Settling(r, now); got != want {
				t.Errorf("days=%d SettlingAt(%q, %q) = %v, Settling = %v",
					days, r, cutoff, got, want)
			}
		}
	}
}
