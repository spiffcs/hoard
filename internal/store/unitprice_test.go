package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func TestOwnedByFinishTellsUnpricedApartFromWorthless(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish nonfoil: %v", err)
	}
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}

	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	byFinish := map[finish.Finish]OwnedFinish{}
	for _, o := range owned {
		byFinish[o.Finish] = o
	}
	if len(byFinish) != 2 {
		t.Fatalf("owned = %+v, want a nonfoil and a foil row", owned)
	}

	nonfoil := byFinish[finish.Nonfoil]
	if nonfoil.UnitPrice == nil {
		t.Fatal("the nonfoil is priced at 0.34; its unit price came back absent")
	}
	if *nonfoil.UnitPrice != 0.34 {
		t.Errorf("nonfoil unit price = %v, want 0.34", *nonfoil.UnitPrice)
	}

	foil := byFinish[finish.Foil]
	if foil.UnitPrice != nil {
		t.Errorf("foil unit price = %v, want absent: this printing has no foil price",
			*foil.UnitPrice)
	}
	if foil.Value != 0 {
		t.Errorf("foil value = %v, want 0", foil.Value)
	}
}
