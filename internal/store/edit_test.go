package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func TestSetHoldingQuantity(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	prev, err := s.SetHoldingQuantity("ulamog-id", finish.Nonfoil, ConditionUnknown, 5)
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

func TestSetHoldingQuantityZeroRemovesTheEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.SetHoldingQuantity("ulamog-id", finish.Nonfoil, ConditionUnknown, 0); err != nil {
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

func TestSetHoldingQuantityHandlesEtched(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Etched, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if _, err := s.SetHoldingQuantity("ulamog-id", finish.Etched, ConditionUnknown, 4); err != nil {
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
	if _, err := s.SetHoldingQuantity("ulamog-id", finish.Nonfoil, ConditionUnknown, 1); err == nil {
		t.Error("want an error for Scryfall's spelling of normal, got nil")
	}
}

func TestRemoveFromCollectionReturnsWhatItRemoved(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCard normal: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 2); err != nil {
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

	if err := s.RestoreHoldings("ulamog-id", removed); err != nil {
		t.Fatalf("RestoreHoldings: %v", err)
	}
	held := heldByFinish(t, s, "ulamog-id")
	if held["nonfoil"] != 3 || held["foil"] != 2 {
		t.Errorf("after restore: %v, want 3 normal and 2 foil", held)
	}
}

func TestRemoveFromCollectionLeavesDecksAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "D", Source: "text", SourceID: "d1"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 2}},
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

func TestRestoreHoldingsReplacesRatherThanAdds(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	removed, err := s.RemoveFromCollection("ulamog-id")
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
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

	if err := s.RestoreHoldings("nobody-id", removed); err != nil {
		t.Errorf("RestoreHoldings on an empty list: %v", err)
	}
}

func TestMoveCardFinishRekeysTheDefaultBinder(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	if _, err := s.MoveCardFinish("ulamog-id", finish.Nonfoil, finish.Foil); err != nil {
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

func TestMoveCardFinishMergesWithAnExistingHolding(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Foil, 2); err != nil {
		t.Fatalf("AddCard foil: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard nonfoil: %v", err)
	}

	prev, err := s.MoveCardFinish("ulamog-id", finish.Nonfoil, finish.Foil)
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

func mainRef(cid int64, id string, fin finish.Finish, cond string) EntryRef {
	return EntryRef{ContainerID: cid, ScryfallID: id, Finish: fin, Condition: cond,
		Board: BoardMain}
}
