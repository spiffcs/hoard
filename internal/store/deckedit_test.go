package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// boardedDeck holds Ulamog as the commander, one Sol Ring in the main deck and
// two more in the sideboard — the same printing and finish on two boards, which
// is what a real decklist looks like and what a board-blind edit corrupts.
func boardedDeck(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	id, err := s.UpsertDeck(
		DeckMeta{Name: "Atraxa", Source: "text", SourceID: "atraxa"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "commander", Quantity: 1},
			{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
			{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "side", Quantity: 2},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return s, id
}

func deckBoards(t *testing.T, s *Store, deckID int64, scryfallID string) map[string]int {
	t.Helper()
	views, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	out := map[string]int{}
	for _, v := range views {
		if v.Card.ScryfallID != scryfallID {
			continue
		}
		if _, clash := out[v.Board]; clash {
			t.Fatalf("two rows for %s on board %q", scryfallID, v.Board)
		}
		out[v.Board] = v.Quantity
	}
	return out
}

func deckRef(deckID int64, scryfallID, board string) EntryRef {
	return EntryRef{ContainerID: deckID, ScryfallID: scryfallID,
		Finish: finish.Nonfoil, Condition: ConditionUnknown, Board: board}
}

func TestRaisingASideboardCountLeavesTheMainboardAlone(t *testing.T) {
	s, deck := boardedDeck(t)

	previous, err := s.SetEntryQuantity(deckRef(deck, "sol-id", "side"), 3)
	if err != nil {
		t.Fatalf("SetEntryQuantity: %v", err)
	}
	if previous != 2 {
		t.Errorf("previous = %d, want 2 — the edit found the wrong row", previous)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if boards["side"] != 3 {
		t.Errorf("sideboard holds %d, want 3", boards["side"])
	}
	if boards["main"] != 1 {
		t.Errorf("main deck holds %d, want the 1 it started with", boards["main"])
	}
	if len(boards) != 2 {
		t.Errorf("Sol Ring is on %d boards (%v), want exactly main and side", len(boards), boards)
	}
}

func TestZeroingASideboardCopyKeepsTheMainboardCopy(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.SetEntryQuantity(deckRef(deck, "sol-id", "side"), 0); err != nil {
		t.Fatalf("SetEntryQuantity: %v", err)
	}

	boards := deckBoards(t, s, deck, "sol-id")
	if _, still := boards["side"]; still {
		t.Errorf("sideboard copy survived removal: %v", boards)
	}
	if boards["main"] != 1 {
		t.Errorf("main deck holds %d, want the 1 it started with", boards["main"])
	}
}

func TestEditingTheCommanderStaysInTheCommandZone(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.SetEntryQuantity(deckRef(deck, "ulamog-id", "commander"), 2); err != nil {
		t.Fatalf("SetEntryQuantity: %v", err)
	}

	boards := deckBoards(t, s, deck, "ulamog-id")
	if boards["commander"] != 2 {
		t.Errorf("command zone holds %d, want 2", boards["commander"])
	}
	if len(boards) != 1 {
		t.Errorf("Ulamog is on %d boards (%v), want only the command zone", len(boards), boards)
	}
}

func TestDeckEditsShowUpInWhatYouOwn(t *testing.T) {
	s, deck := boardedDeck(t)

	if _, err := s.SetEntryQuantity(deckRef(deck, "sol-id", "side"), 5); err != nil {
		t.Fatalf("SetEntryQuantity: %v", err)
	}

	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	copies := map[string]int{}
	for _, o := range owned {
		copies[o.ScryfallID] = o.Copies
	}
	if copies["sol-id"] != 6 {
		t.Errorf("you own %d Sol Rings, want 6 — 1 in the deck and 5 in the side",
			copies["sol-id"])
	}
}
