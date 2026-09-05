package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func repinFixture(t *testing.T, entries []Entry) (*Store, int64) {
	t.Helper()
	s := newTestStore(t)
	hob := scryfall.Card{ID: "we-hob", Set: "hob", CollectorNumber: "142",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	cma := scryfall.Card{ID: "we-cma", Set: "cma", CollectorNumber: "154",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	if err := s.UpsertPrintings([]scryfall.Card{hob, cma}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	id, err := s.UpsertDeck(
		DeckMeta{Name: "Guided", Source: "text", SourceID: "guided"}, entries)
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return s, id
}

func paidByQuantity(t *testing.T, s *Store, deckID int64) map[float64]int {
	t.Helper()
	entries, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	out := map[float64]int{}
	for _, e := range entries {
		if e.PurchasePrice == nil {
			t.Errorf("%s (%s) came back with no recorded cost",
				e.Card.Name, e.Card.SetCode)
			continue
		}
		out[*e.PurchasePrice] += e.Quantity
	}
	return out
}

func TestRepinKeepsWhatYouPaid(t *testing.T) {
	s, deckID := repinFixture(t, []Entry{{ScryfallID: "we-hob", Finish: finish.Nonfoil,
		Board: "main", Quantity: 2, PurchasePrice: f(12.50)}})

	if _, err := s.RepointDeckPrintings(deckID, map[string]string{"we-hob": "we-cma"}); err != nil {
		t.Fatalf("RepointDeckPrintings: %v", err)
	}

	entries, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("deck entries = %+v, want one row", entries)
	}
	if got := entries[0].Card.ScryfallID; got != "we-cma" {
		t.Errorf("printing = %s, want we-cma", got)
	}
	if entries[0].PurchasePrice == nil {
		t.Fatal("re-pinning erased what the copies cost")
	}
	if got := *entries[0].PurchasePrice; got != 12.50 {
		t.Errorf("purchase price = %v, want 12.50", got)
	}
}

func TestRepinKeepsLotsBoughtAtDifferentPricesApart(t *testing.T) {
	s, deckID := repinFixture(t, []Entry{
		{ScryfallID: "we-hob", Finish: finish.Nonfoil, Board: "main",
			Quantity: 4, PurchasePrice: f(2)},
		{ScryfallID: "we-hob", Finish: finish.Nonfoil, Board: "main",
			Quantity: 2, PurchasePrice: f(9)},
	})
	if before := paidByQuantity(t, s, deckID); len(before) != 2 {
		t.Fatalf("fixture did not store two lots: %v", before)
	}

	if _, err := s.RepointDeckPrintings(deckID, map[string]string{"we-hob": "we-cma"}); err != nil {
		t.Fatalf("RepointDeckPrintings: %v", err)
	}

	got := paidByQuantity(t, s, deckID)
	if len(got) != 2 || got[2] != 4 || got[9] != 2 {
		t.Errorf("lots after re-pinning = %v, want 4 copies at 2 and 2 copies at 9", got)
	}
}

func TestRepinStillMergesLotsWithNoRecordedCost(t *testing.T) {
	s, deckID := repinFixture(t, []Entry{
		{ScryfallID: "we-hob", Finish: finish.Nonfoil, Board: "main", Quantity: 2},
		{ScryfallID: "we-cma", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
	})

	if _, err := s.RepointDeckPrintings(deckID, map[string]string{"we-hob": "we-cma"}); err != nil {
		t.Fatalf("RepointDeckPrintings: %v", err)
	}

	entries, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Quantity != 3 {
		t.Fatalf("entries = %+v, want one merged row of 3", entries)
	}
}
