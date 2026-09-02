package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func ulamogPromo() scryfall.Card {
	c := ulamog()
	c.ID = "ulamog-promo-id"
	c.Set = "prm"
	c.CollectorNumber = "42"
	return c
}

func placesOf(t *testing.T, s *Store, name string) map[string][]string {
	t.Helper()
	places, err := s.WhereHeld()
	if err != nil {
		t.Fatalf("WhereHeld: %v", err)
	}
	out := map[string][]string{}
	for _, p := range places {
		if p.Name == name {
			out[p.Finish.String()] = append(out[p.Finish.String()], p.ContainerName)
		}
	}
	return out
}

func TestWhereHeldNamesEveryCountedContainerHoldingACardName(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	trade, err := s.CreateBinder("Trade Stock")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(trade, ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := s.AddCardFinishTo(trade, ulamogPromo(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 3}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	vault, err := s.CreateBinder("Vault")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(vault, ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := s.SetContainerCounted(vault, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}

	got := placesOf(t, s, "Ulamog, the Infinite Gyre")

	wantNonfoil := []string{LooseName, "Trade Stock", "Fish"}
	if !slices.Equal(got["nonfoil"], wantNonfoil) {
		t.Errorf("nonfoil places = %q, want %q — both printings count, binders before decks",
			got["nonfoil"], wantNonfoil)
	}
	if want := []string{"Trade Stock"}; !slices.Equal(got["foil"], want) {
		t.Errorf("foil places = %q, want %q — the nonfoil copies are a different row", got["foil"], want)
	}

	places, err := s.WhereHeld()
	if err != nil {
		t.Fatalf("WhereHeld: %v", err)
	}
	for _, p := range places {
		if p.ContainerName == "Vault" {
			t.Errorf("Vault is uncounted, so it holds nothing All Cards shows: %+v", p)
		}
	}
}
