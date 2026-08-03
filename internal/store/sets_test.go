package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// seedSets stages holdings across three sets: two with Scryfall documents
// (so set_name/released_at resolve) and one bare printing whose generated
// columns read NULL — the decklist-import case.
func seedSets(t *testing.T, s *Store) {
	t.Helper()
	mh2 := scryfall.Card{
		ID: "sol-mh2", Set: "mh2", CollectorNumber: "1", Name: "Solitude",
		ScryfallURL: "http://x", PriceUSD: f(30),
		Raw: []byte(`{"set_name":"Modern Horizons 2","released_at":"2021-06-18"}`),
	}
	uma := scryfall.Card{
		ID: "bb-uma", Set: "uma", CollectorNumber: "2", Name: "Bitterblossom",
		ScryfallURL: "http://x", PriceUSD: f(20), PriceUSDFoil: f(60),
		Raw: []byte(`{"set_name":"Ultimate Masters","released_at":"2018-12-07"}`),
	}
	bare := scryfall.Card{
		ID: "myst-zzz", Set: "zzz", CollectorNumber: "3", Name: "Mystery",
		ScryfallURL: "http://x", PriceUSD: f(1),
	}
	if err := s.AddCardFinish(mh2, "nonfoil", 2); err != nil {
		t.Fatalf("adding mh2: %v", err)
	}
	if err := s.AddCardFinish(uma, "nonfoil", 1); err != nil {
		t.Fatalf("adding uma nonfoil: %v", err)
	}
	if err := s.AddCardFinish(uma, "foil", 1); err != nil {
		t.Fatalf("adding uma foil: %v", err)
	}
	if err := s.AddCardFinish(bare, "nonfoil", 3); err != nil {
		t.Fatalf("adding bare: %v", err)
	}
	// The mh2 printing again in a second binder: a set's rollup spans
	// containers.
	bid, err := s.CreateBinder("Trades")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(bid, mh2, "nonfoil", 1); err != nil {
		t.Fatalf("adding mh2 to binder: %v", err)
	}
}

// SetsHeld rolls the hoard up by set: newest release first, the undated
// bare set last under its upper-cased code, copies and value summed across
// every container.
func TestSetsHeld(t *testing.T) {
	s := newTestStore(t)
	seedSets(t, s)

	sets, err := s.SetsHeld()
	if err != nil {
		t.Fatalf("SetsHeld: %v", err)
	}
	if len(sets) != 3 {
		t.Fatalf("sets = %+v, want 3", sets)
	}
	if sets[0].Code != "mh2" || sets[1].Code != "uma" || sets[2].Code != "zzz" {
		t.Fatalf("order = %s %s %s, want mh2 uma zzz (newest first, undated last)",
			sets[0].Code, sets[1].Code, sets[2].Code)
	}
	if sets[0].Name != "Modern Horizons 2" || sets[0].ReleasedAt != "2021-06-18" {
		t.Errorf("mh2 = %+v, want the pretty name and date", sets[0])
	}
	if sets[2].Name != "ZZZ" || sets[2].ReleasedAt != "" {
		t.Errorf("bare set = %+v, want the upper-cased code and no date", sets[2])
	}
	if sets[0].Copies != 3 || sets[0].Value != 90 {
		t.Errorf("mh2 rollup = %d copies $%.0f, want 3 copies $90 across both binders",
			sets[0].Copies, sets[0].Value)
	}
	if sets[1].Copies != 2 || sets[1].Value != 80 {
		t.Errorf("uma rollup = %d copies $%.0f, want 2 copies $80 (nonfoil + foil)",
			sets[1].Copies, sets[1].Value)
	}
}

// SetByFinish is AllByFinish narrowed to one set: only its rows, one per
// printing and finish, quantities summed across containers.
func TestSetByFinish(t *testing.T) {
	s := newTestStore(t)
	seedSets(t, s)

	rows, err := s.SetByFinish("uma")
	if err != nil {
		t.Fatalf("SetByFinish: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("uma rows = %+v, want the two finishes", rows)
	}
	for _, r := range rows {
		if r.SetCode != "uma" {
			t.Errorf("row %s from set %s leaked into uma", r.Name, r.SetCode)
		}
	}

	rows, err = s.SetByFinish("mh2")
	if err != nil {
		t.Fatalf("SetByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 3 || rows[0].Value != 90 {
		t.Fatalf("mh2 rows = %+v, want one row with both binders' copies summed", rows)
	}
}
