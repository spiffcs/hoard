package store

import "testing"

func TestMovingASideboardCopyIntoTheMainDeckMergesWithIt(t *testing.T) {
	s, deck := boardedDeck(t)

	prevTarget, err := s.MoveEntryBoard(deckRef(deck, "sol-id", "side"), "main", 2)
	if err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}
	if prevTarget != 1 {
		t.Errorf("prevTarget = %d, want the 1 the main deck already held", prevTarget)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if boards["main"] != 3 {
		t.Errorf("main deck holds %d Sol Ring, want the 1 plus the 2 moved over", boards["main"])
	}
	if _, still := boards["side"]; still {
		t.Errorf("Sol Ring is still on the sideboard: %v", boards)
	}
}

func TestMovingACopyToAnEmptyBoardLeavesTheOtherCardsAlone(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.MoveEntryBoard(deckRef(deck, "ulamog-id", "commander"), "side", 1); err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}

	ulamog := deckBoards(t, s, deck, "ulamog-id")
	if ulamog["side"] != 1 {
		t.Errorf("Ulamog is on %v, want one copy on the sideboard", ulamog)
	}
	if len(ulamog) != 1 {
		t.Errorf("Ulamog is on %d boards (%v), want only the sideboard", len(ulamog), ulamog)
	}

	sol := deckBoards(t, s, deck, "sol-id")
	if sol["main"] != 1 || sol["side"] != 2 {
		t.Errorf("Sol Ring is now %v, want main 1 and side 2 untouched", sol)
	}
}

func TestMovingToTheBoardACardIsAlreadyOnChangesNothing(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.MoveEntryBoard(deckRef(deck, "sol-id", "side"), "side", 2); err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}

	sol := deckBoards(t, s, deck, "sol-id")
	if sol["main"] != 1 || sol["side"] != 2 {
		t.Errorf("Sol Ring is now %v, want main 1 and side 2 untouched", sol)
	}
}

func TestUndoingABoardMovePutsBothRowsBack(t *testing.T) {
	s, deck := boardedDeck(t)
	from := deckRef(deck, "sol-id", "side")

	prevTarget, err := s.MoveEntryBoard(from, "main", 2)
	if err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}

	if _, err := s.SetEntryQuantity(deckRef(deck, "sol-id", "main"), prevTarget); err != nil {
		t.Fatalf("restoring the main deck: %v", err)
	}
	if _, err := s.SetEntryQuantity(from, 2); err != nil {
		t.Fatalf("restoring the sideboard: %v", err)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if boards["main"] != 1 || boards["side"] != 2 {
		t.Errorf("Sol Ring is %v, want the main 1 and side 2 it started with", boards)
	}
}

func TestMovingOneCopyLeavesTheRestOnItsBoard(t *testing.T) {
	s, deck := boardedDeck(t)

	prevTarget, err := s.MoveEntryBoard(deckRef(deck, "sol-id", "side"), "main", 1)
	if err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}
	if prevTarget != 1 {
		t.Errorf("prevTarget = %d, want the 1 the main deck already held", prevTarget)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if boards["main"] != 2 {
		t.Errorf("the main deck holds %d, want the 1 plus the single copy moved", boards["main"])
	}
	if boards["side"] != 1 {
		t.Errorf("the sideboard holds %d, want the 1 left behind", boards["side"])
	}
}

func TestMovingTheLastCopyEmptiesTheSourceBoard(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.MoveEntryBoard(deckRef(deck, "sol-id", "main"), "side", 1); err != nil {
		t.Fatalf("MoveEntryBoard: %v", err)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if _, still := boards["main"]; still {
		t.Errorf("the emptied main deck row survived: %v", boards)
	}
	if boards["side"] != 3 {
		t.Errorf("the sideboard holds %d, want the 2 plus the 1 moved", boards["side"])
	}
}
