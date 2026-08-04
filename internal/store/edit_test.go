package store

import "testing"

func TestSetHoldingQuantity(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	prev, err := s.SetHoldingQuantity("ulamog-id", "nonfoil", 5)
	if err != nil {
		t.Fatalf("SetHoldingQuantity: %v", err)
	}
	if prev != 2 {
		t.Errorf("previous = %d, want 2", prev)
	}
	if held := heldByFinish(t, s, "ulamog-id"); held["nonfoil"] != 5 {
		t.Errorf("normal = %d, want 5", held["nonfoil"])
	}
}

// Zero deletes rather than storing a zero, so "held in no copies" and "not
// held" stay one state. A stored zero would show up in every listing that
// counts holdings.
func TestSetHoldingQuantityZeroRemovesTheEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.SetHoldingQuantity("ulamog-id", "nonfoil", 0); err != nil {
		t.Fatalf("SetHoldingQuantity: %v", err)
	}

	if held := heldByFinish(t, s, "ulamog-id"); len(held) != 0 {
		t.Errorf("holdings = %v, want none", held)
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM card_entries WHERE scryfall_id = 'ulamog-id'`).Scan(&n); err != nil {
		t.Fatalf("counting entries: %v", err)
	}
	if n != 0 {
		t.Errorf("%d entry rows remain, want the row deleted rather than zeroed", n)
	}
}

// The reason this is per-finish and not the normal/foil pair the removed CLI
// used: etched is a finish of its own everywhere else, and a pivoted setter
// leaves etched entries untouched while appearing to have set the quantities.
func TestSetHoldingQuantityHandlesEtched(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if _, err := s.SetHoldingQuantity("ulamog-id", "etched", 4); err != nil {
		t.Fatalf("SetHoldingQuantity etched: %v", err)
	}
	held := heldByFinish(t, s, "ulamog-id")
	if held["etched"] != 4 {
		t.Errorf("etched = %d, want 4", held["etched"])
	}
	if held["foil"] != 1 {
		t.Errorf("foil = %d, want 1 — adjusting etched must not disturb it", held["foil"])
	}
}

func TestSetHoldingQuantityRejectsUnknownFinish(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SetHoldingQuantity("ulamog-id", "nonfoil", 1); err == nil {
		t.Error("want an error for Scryfall's spelling of normal, got nil")
	}
}

// A card can be held in several finishes at once, so what was removed is a
// list, not a number — re-adding one copy of one finish would not restore it.
func TestRemoveFromCollectionReturnsWhatItRemoved(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCard normal: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "foil", 2); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}

	removed, err := s.RemoveFromCollection("ulamog-id")
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %+v, want both finishes", removed)
	}
	if held := heldByFinish(t, s, "ulamog-id"); len(held) != 0 {
		t.Errorf("holdings = %v, want none after removal", held)
	}

	// And the removal round-trips: this is what makes undo possible.
	if err := s.RestoreHoldings("ulamog-id", removed); err != nil {
		t.Fatalf("RestoreHoldings: %v", err)
	}
	held := heldByFinish(t, s, "ulamog-id")
	if held["nonfoil"] != 3 || held["foil"] != 2 {
		t.Errorf("after restore: %v, want 3 normal and 2 foil", held)
	}
}

// A card in a deck is held by that deck. Removing it from the loose collection
// must not quietly empty a decklist imported from somewhere else.
func TestRemoveFromCollectionLeavesDecksAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "D", Source: "text", SourceID: "d1"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "main", Quantity: 2}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	if _, err := s.RemoveFromCollection("ulamog-id"); err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}

	holdings, err := s.HoldingsOf("ulamog-id")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 1 || holdings[0].ContainerKind != KindDeck || holdings[0].Quantity != 2 {
		t.Errorf("holdings = %+v, want the deck's 2 copies untouched", holdings)
	}
}

// Undo restores the state that was there. Adding instead of replacing would
// double a holding that was partly re-added between the removal and the undo.
func TestRestoreHoldingsReplacesRatherThanAdds(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	removed, err := s.RemoveFromCollection("ulamog-id")
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	// Someone re-adds a copy by hand before hitting undo.
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard again: %v", err)
	}
	if err := s.RestoreHoldings("ulamog-id", removed); err != nil {
		t.Fatalf("RestoreHoldings: %v", err)
	}
	if held := heldByFinish(t, s, "ulamog-id"); held["nonfoil"] != 3 {
		t.Errorf("normal = %d, want the 3 that were removed, not 4", held["nonfoil"])
	}
}

func TestRemoveFromCollectionOnUnheldCard(t *testing.T) {
	s := newTestStore(t)
	removed, err := s.RemoveFromCollection("nobody-id")
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v, want none", removed)
	}
	// Restoring nothing is a no-op, not an error — undo after a removal that
	// found nothing must not fail.
	if err := s.RestoreHoldings("nobody-id", removed); err != nil {
		t.Errorf("RestoreHoldings on an empty list: %v", err)
	}
}

// The scan flow's finish correction: a foil whose marker would not read on the
// first capture lands as the nonfoil default, and the next look re-keys that
// row rather than adding a second one beside it.
func TestMoveCardFinishRekeysTheDefaultBinder(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	if _, err := s.MoveCardFinish("ulamog-id", "nonfoil", "foil"); err != nil {
		t.Fatalf("MoveCardFinish: %v", err)
	}

	held := heldByFinish(t, s, "ulamog-id")
	if held["foil"] != 1 {
		t.Errorf("foil = %d, want 1", held["foil"])
	}
	if _, ok := held["nonfoil"]; ok {
		t.Errorf("holdings = %v, want the nonfoil row gone, not left at zero", held)
	}
}

// Correcting into a finish already held merges instead of colliding — the same
// card can legitimately be owned both ways.
func TestMoveCardFinishMergesWithAnExistingHolding(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "foil", 2); err != nil {
		t.Fatalf("AddCard foil: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard nonfoil: %v", err)
	}

	prev, err := s.MoveCardFinish("ulamog-id", "nonfoil", "foil")
	if err != nil {
		t.Fatalf("MoveCardFinish: %v", err)
	}
	if prev != 2 {
		t.Errorf("previous foil quantity = %d, want 2 so an undo can split them back", prev)
	}
	if held := heldByFinish(t, s, "ulamog-id"); held["foil"] != 3 {
		t.Errorf("foil = %d, want 3", held["foil"])
	}
}
