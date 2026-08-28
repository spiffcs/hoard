package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

func statsRowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM card_trend_stats`).Scan(&n)
	if err != nil {
		t.Fatalf("counting card_trend_stats: %v", err)
	}
	return n
}

func TestTrendsLeaveTheirStatsBehind(t *testing.T) {
	s := trendMixStore(t)

	if n := statsRowCount(t, s); n != 0 {
		t.Fatalf("a fresh hoard already holds %d stat rows", n)
	}

	dips, momentum, err := s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if n := statsRowCount(t, s); n == 0 {
		t.Fatal("Trends stored nothing; every later call must scan the history again")
	}

	again, againMomentum, err := s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends again: %v", err)
	}
	if !slices.Equal(namesInOrder(dips), namesInOrder(again)) {
		t.Errorf("second call dips = %v, want the first call's %v",
			namesInOrder(again), namesInOrder(dips))
	}
	if !slices.Equal(namesInOrder(momentum), namesInOrder(againMomentum)) {
		t.Errorf("second call momentum = %v, want the first call's %v",
			namesInOrder(againMomentum), namesInOrder(momentum))
	}
}

func TestARecordedPriceIsNeverServedFromStaleStats(t *testing.T) {
	s := trendMixStore(t)

	_, momentum, err := s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if _, ok := rowNamed(momentum, "Steady Climber"); !ok {
		t.Fatalf("Steady Climber should start out climbing, got %v", namesInOrder(momentum))
	}

	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	_, momentum, err = s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends after recording: %v", err)
	}
	if _, ok := rowNamed(momentum, "Steady Climber"); ok {
		t.Errorf("momentum = %v, want Steady Climber gone — it just took a step down",
			namesInOrder(momentum))
	}
}

func TestBackfilledHistoryIsNeverServedFromStaleStats(t *testing.T) {
	s := trendMixStore(t)
	o := trendOpts()
	o.Since = "2026-05-01T00:00:00Z"

	dips, _, err := s.Trends(o)
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	dipper, ok := rowNamed(dips, "Deep Dipper")
	if !ok {
		t.Fatalf("Deep Dipper missing from %v", namesInOrder(dips))
	}
	if dipper.High != 100 {
		t.Fatalf("Deep Dipper starts at high %v, want 100", dipper.High)
	}

	older := []mtgjson.Observation{
		{Date: "2026-05-20", Finish: dipper.Finish, Price: 400, Source: "tcgplayer"},
		{Date: "2026-05-21", Finish: dipper.Finish, Price: 380, Source: "tcgplayer"},
	}
	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{"dipper": older}); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

	dips, _, err = s.Trends(o)
	if err != nil {
		t.Fatalf("Trends after backfill: %v", err)
	}
	dipper, ok = rowNamed(dips, "Deep Dipper")
	if !ok {
		t.Fatalf("Deep Dipper vanished after the backfill: %v", namesInOrder(dips))
	}
	if dipper.High != 400 {
		t.Errorf("Deep Dipper high = %v, want 400 — prepended history moves the high, "+
			"and it never changes the newest row the stats were stamped against",
			dipper.High)
	}
}
