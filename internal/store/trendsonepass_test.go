package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

func trendMixStore(t *testing.T) *Store {
	t.Helper()
	return trendStore(t,
		map[string][]mtgjson.Observation{
			"dipper":  daily(100, 90, 80, 72, 70),
			"deeper":  daily(200, 150, 120, 100, 95),
			"climber": daily(10, 12, 14, 16, 18, 20),
			"slower":  daily(30, 31, 32, 33),
			"wobbler": daily(10, 12, 11, 13, 15, 17),
		},
		trendCard("dipper", "Deep Dipper", "2020-01-01"),
		trendCard("deeper", "Deeper Still", "2020-01-01"),
		trendCard("climber", "Steady Climber", "2020-01-01"),
		trendCard("slower", "Shorter Climb", "2020-01-01"),
		trendCard("wobbler", "One Step Back", "2020-01-01"),
	)
}

func namesInOrder(rows []TrendRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func rowNamed(rows []TrendRow, name string) (TrendRow, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return TrendRow{}, false
}

func TestTrendsReturnsBothSetsInTheirOwnOrder(t *testing.T) {
	s := trendMixStore(t)

	dips, momentum, err := s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}

	if want := []string{"Deeper Still", "Deep Dipper"}; !slices.Equal(namesInOrder(dips), want) {
		t.Errorf("dips = %v, want %v — furthest off its high first",
			namesInOrder(dips), want)
	}
	if want := []string{"Steady Climber", "Shorter Climb"}; !slices.Equal(namesInOrder(momentum), want) {
		t.Errorf("momentum = %v, want %v — longest streak first",
			namesInOrder(momentum), want)
	}
	for _, set := range [][]TrendRow{dips, momentum} {
		if _, found := rowNamed(set, "One Step Back"); found {
			t.Error("a card that is neither at its floor nor cleanly climbing was returned")
		}
	}
}

func TestTrendsCarriesTheSameRowDetailAsBefore(t *testing.T) {
	s := trendMixStore(t)

	dips, momentum, err := s.Trends(trendOpts())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}

	dip, ok := rowNamed(dips, "Deep Dipper")
	if !ok {
		t.Fatalf("Deep Dipper missing from dips %v", namesInOrder(dips))
	}
	if dip.First != 100 || dip.Last != 70 || dip.Low != 70 || dip.High != 100 {
		t.Errorf("Deep Dipper = first %v last %v low %v high %v, want 100/70/70/100",
			dip.First, dip.Last, dip.Low, dip.High)
	}

	climb, ok := rowNamed(momentum, "Steady Climber")
	if !ok {
		t.Fatalf("Steady Climber missing from momentum %v", namesInOrder(momentum))
	}
	if climb.First != 10 || climb.Last != 20 {
		t.Errorf("Steady Climber endpoints = %v → %v, want 10 → 20", climb.First, climb.Last)
	}
	if climb.Ups != 5 || climb.Downs != 0 || climb.Moves != 5 {
		t.Errorf("Steady Climber streak = %d up / %d down / %d moves, want 5/0/5",
			climb.Ups, climb.Downs, climb.Moves)
	}
}

func TestTrendsHonoursTheSameFiltersAsDips(t *testing.T) {
	s := trendMixStore(t)

	o := trendOpts()
	o.MinPrice = 80
	dips, _, err := s.Trends(o)
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if want := []string{"Deeper Still"}; !slices.Equal(namesInOrder(dips), want) {
		t.Errorf("dips under a $80 floor = %v, want %v", namesInOrder(dips), want)
	}

	o = trendOpts()
	o.SettledBefore = "2026-07-01"
	fresh := trendStore(t,
		map[string][]mtgjson.Observation{"newset": daily(100, 90, 80, 72, 70)},
		trendCard("newset", "Fresh Print", "2026-07-15"))
	dips, _, err = fresh.Trends(o)
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if len(dips) != 0 {
		t.Errorf("dips = %v, want none — an unsettled set must be held out",
			namesInOrder(dips))
	}
}
