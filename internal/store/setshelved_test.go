package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func shelvedSolRing(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	want, err := s.CreateBinder("Want")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(want, solRing(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinishTo(nonfoil): %v", err)
	}
	if err := s.AddCardFinishTo(want, solRing(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinishTo(foil): %v", err)
	}
	if err := s.SetContainerCounted(want, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	if err := s.AddCardFinish(solRing(), finish.Etched, 1); err != nil {
		t.Fatalf("AddCardFinish(etched): %v", err)
	}
	return s
}

func TestSetShelvedByFinishSplitsAWantListByFinish(t *testing.T) {
	s := shelvedSolRing(t)

	rows, err := s.SetShelvedByFinish("c21")
	if err != nil {
		t.Fatalf("SetShelvedByFinish(c21): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("SetShelvedByFinish(c21) = %d rows, want 2 — the nonfoils and the foil "+
			"are different cards to buy, and the etched copy you count is neither",
			len(rows))
	}

	byFinish := map[finish.Finish]UnownedRow{}
	for _, r := range rows {
		byFinish[r.Finish] = r
	}
	nonfoil, ok := byFinish[finish.Nonfoil]
	if !ok {
		t.Fatalf("no nonfoil row in %+v", rows)
	}
	if nonfoil.Quantity != 2 || nonfoil.Where != "Want" {
		t.Errorf("nonfoil row = %d copies in %q, want 2 in Want",
			nonfoil.Quantity, nonfoil.Where)
	}
	foil, ok := byFinish[finish.Foil]
	if !ok {
		t.Fatalf("no foil row in %+v", rows)
	}
	if foil.Quantity != 1 || foil.Where != "Want" {
		t.Errorf("foil row = %d copies in %q, want 1 in Want", foil.Quantity, foil.Where)
	}
	if nonfoil.Price() == nil || *nonfoil.Price() != 2.00 {
		t.Errorf("nonfoil row costs %v, want Sol Ring's $2.00 nonfoil price",
			nonfoil.Price())
	}
	if foil.Price() == nil || *foil.Price() != 5.00 {
		t.Errorf("foil row costs %v, want the $5.00 foil price, not the nonfoil's $2.00",
			foil.Price())
	}
}
