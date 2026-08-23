package store

import (
	"fmt"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func trendCard(id, name, released string) scryfall.Card {
	return scryfall.Card{
		ID: id, Set: "tst", CollectorNumber: "1", Name: name,
		PriceUSD:    f(1),
		ScryfallURL: "https://scryfall.com/card/tst/1",
		Raw:         []byte(fmt.Sprintf(`{"released_at":%q,"rarity":"rare"}`, released)),
	}
}

func daily(prices ...float64) []mtgjson.Observation {
	obs := make([]mtgjson.Observation, len(prices))
	for i, p := range prices {
		obs[i] = mtgjson.Observation{
			Date:   fmt.Sprintf("2026-06-%02d", i+1),
			Finish: finish.Nonfoil, Price: p, Source: "tcgplayer",
		}
	}
	return obs
}

func trendStore(t *testing.T, series map[string][]mtgjson.Observation,
	cards ...scryfall.Card) *Store {
	t.Helper()
	s := newTestStore(t)
	cid, err := s.CollectionID()
	if err != nil {
		t.Fatalf("CollectionID: %v", err)
	}
	ps := make([]CompendiumPrinting, len(cards))
	for i, c := range cards {
		ps[i] = CompendiumPrinting{Card: c, Finishes: []finish.Finish{finish.Nonfoil}}
	}
	if _, _, err := s.SeedCompendiumPrintings(cid, ps); err != nil {
		t.Fatalf("SeedCompendiumPrintings: %v", err)
	}
	if _, _, err := s.BackfillPrices(series); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	return s
}

func trendNames(rows []TrendRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	slices.Sort(out)
	return out
}

func trendOpts() TrendOptions {
	return TrendOptions{Since: "2026-06-01T00:00:00Z",
		SettledBefore: "2026-03-01", MinChecks: 4}
}

func TestDipsFindsOnlyCardsSittingAtTheirFloor(t *testing.T) {
	s := trendStore(t,
		map[string][]mtgjson.Observation{
			"dipper":  daily(100, 90, 80, 72, 70),
			"shallow": daily(50, 51, 50.5, 49.9, 50),
			"rebound": daily(100, 80, 60, 75, 90),
			"newset":  daily(100, 90, 80, 72, 70),
		},
		trendCard("dipper", "Deep Dipper", "2020-01-01"),
		trendCard("shallow", "Barely Moved", "2020-01-01"),
		trendCard("rebound", "Already Bouncing", "2020-01-01"),
		trendCard("newset", "Fresh Print", "2026-05-20"),
	)

	got, err := s.Dips(trendOpts())
	if err != nil {
		t.Fatalf("Dips: %v", err)
	}
	if want := []string{"Deep Dipper"}; !slices.Equal(trendNames(got), want) {
		t.Fatalf("Dips = %v, want %v — only a card at its floor AND well off its high",
			trendNames(got), want)
	}

	r := got[0]
	if r.High != 100 || r.Low != 70 || r.Last != 70 {
		t.Errorf("row = high %v low %v last %v, want 100/70/70", r.High, r.Low, r.Last)
	}
	if r.Finish != finish.Nonfoil {
		t.Errorf("finish = %v, want nonfoil", r.Finish)
	}
}

func TestMomentumRanksOnlyCleanRisingStreaks(t *testing.T) {
	s := trendStore(t,
		map[string][]mtgjson.Observation{
			"climber": daily(10, 12, 14, 16, 18, 20),
			"wobbler": daily(10, 12, 11, 13, 15, 17),
			"faller":  daily(20, 18, 16, 14, 12, 10),
			"slower":  daily(30, 31, 32, 33),
		},
		trendCard("climber", "Steady Climber", "2020-01-01"),
		trendCard("wobbler", "One Step Back", "2020-01-01"),
		trendCard("faller", "Falling Knife", "2020-01-01"),
		trendCard("slower", "Shorter Climb", "2020-01-01"),
	)

	got, err := s.Momentum(trendOpts())
	if err != nil {
		t.Fatalf("Momentum: %v", err)
	}
	if want := []string{"Shorter Climb", "Steady Climber"}; !slices.Equal(trendNames(got), want) {
		t.Fatalf("Momentum = %v, want %v — reversals and decliners must be excluded",
			trendNames(got), want)
	}

	var climber TrendRow
	for _, r := range got {
		if r.Name == "Steady Climber" {
			climber = r
		}
	}
	if climber.Ups != 5 || climber.Moves != 5 || climber.Downs != 0 {
		t.Errorf("streak = %d up / %d down / %d moves, want 5/0/5",
			climber.Ups, climber.Downs, climber.Moves)
	}
	if climber.First != 10 || climber.Last != 20 {
		t.Errorf("endpoints = %v → %v, want 10 → 20", climber.First, climber.Last)
	}
	if got[0].Name != "Steady Climber" {
		t.Errorf("order = %v, want the longest streak first", trendNames(got))
	}
}

func TestTrendsHonourTheMinimumPrice(t *testing.T) {
	s := trendStore(t,
		map[string][]mtgjson.Observation{
			"cheap": daily(1.00, 0.90, 0.80, 0.72, 0.70),
			"dear":  daily(100, 90, 80, 72, 70),
		},
		trendCard("cheap", "Penny Dip", "2020-01-01"),
		trendCard("dear", "Real Dip", "2020-01-01"),
	)

	o := trendOpts()
	o.MinPrice = 10
	got, err := s.Dips(o)
	if err != nil {
		t.Fatalf("Dips: %v", err)
	}
	if want := []string{"Real Dip"}; !slices.Equal(trendNames(got), want) {
		t.Errorf("Dips = %v, want %v — the floor must drop penny cards", trendNames(got), want)
	}
}
