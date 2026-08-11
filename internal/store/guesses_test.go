package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// The guess log is per commit, not per bucket: two guessed copies of one
// printing are two cards to go check, and clearing one answers for one.
func TestFinishGuessRoundTrip(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "brainsurge-id", Name: "Brainsurge", Set: "mh3",
		CollectorNumber: "399", ScryfallURL: "https://scryfall.com/card/mh3/399"}
	if err := s.AddCardFinish(card, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}
	for range 2 {
		if err := s.RecordFinishGuess(cid, card.ID, "nonfoil"); err != nil {
			t.Fatalf("RecordFinishGuess: %v", err)
		}
	}

	rows, err := s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("guesses = %d, want 2 — one per guessed commit", len(rows))
	}
	if rows[0].Name != "Brainsurge" || rows[0].Finish != "nonfoil" {
		t.Errorf("row = %+v, want the card joined in", rows[0])
	}

	// Evidence arrived for one copy: exactly one row goes.
	if err := s.ClearFinishGuess(cid, card.ID, "nonfoil"); err != nil {
		t.Fatalf("ClearFinishGuess: %v", err)
	}
	rows, err = s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("guesses after one clear = %d, want 1", len(rows))
	}

	// Clearing a guess that was never recorded is a no-op, not an error: the
	// correction path runs for evidence-backed rows too.
	if err := s.ClearFinishGuess(cid, card.ID, "foil"); err != nil {
		t.Errorf("ClearFinishGuess of nothing: %v", err)
	}
}

// The other way a guess is answered, and the one that decides whether this is
// a worklist or a growing ledger: the card was checked and the scanner had it
// right. A correction cannot cover this — nothing gets re-keyed — so without
// it a correct guess is never retired and the list can only grow.
func TestConfirmFinishGuessRetiresExactlyTheNamedRow(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "whisperer", Name: "Primal Whisperer", Set: "lgn",
		CollectorNumber: "135", ScryfallURL: "https://scryfall.com/card/lgn/135"}
	if err := s.AddCardFinish(card, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}
	// Two copies, two commits: the rows are identical in every column, which
	// is why a confirmation is keyed on the id and not on the card.
	for range 2 {
		if err := s.RecordFinishGuess(cid, card.ID, "nonfoil"); err != nil {
			t.Fatalf("RecordFinishGuess: %v", err)
		}
	}
	rows, err := s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("guesses = %d, want 2", len(rows))
	}

	ok, err := s.ConfirmFinishGuess(rows[0].ID)
	if err != nil {
		t.Fatalf("ConfirmFinishGuess: %v", err)
	}
	if !ok {
		t.Errorf("ConfirmFinishGuess reported nothing retired, want the row it named")
	}
	left, err := s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(left) != 1 || left[0].ID != rows[1].ID {
		t.Fatalf("remaining = %+v, want only the other copy (#%d)", left, rows[1].ID)
	}

	// The queue reaches zero once every card has been looked at. An
	// append-only log cannot, and that is the difference being pinned.
	if _, err := s.ConfirmFinishGuess(rows[1].ID); err != nil {
		t.Fatalf("ConfirmFinishGuess: %v", err)
	}
	if left, _ := s.GuessedFinishes(); len(left) != 0 {
		t.Errorf("remaining = %+v, want an empty queue", left)
	}

	// An id naming nothing is reported, not an error: it is how a caller tells
	// a retired guess from a stale id typed off an old listing.
	ok, err = s.ConfirmFinishGuess(rows[0].ID)
	if err != nil {
		t.Errorf("ConfirmFinishGuess of nothing: %v", err)
	}
	if ok {
		t.Errorf("ConfirmFinishGuess of an already-retired id reported a retirement")
	}
}

// The browse promise: fixing a wrong finish in the detail editor retires the
// guess it corrects, so `hoard guessed` drains as the pile gets checked.
func TestMoveEntryFinishClearsTheGuess(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "digger-id", Name: "Trap Digger", Set: "mmq",
		CollectorNumber: "50", Finishes: []string{"nonfoil", "foil"},
		ScryfallURL: "https://scryfall.com/card/mmq/50"}
	if err := s.AddCardFinish(card, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}
	// The scan's sentinel for "the default binder" resolves to the real
	// container, so the editor's real id can find the row again.
	if err := s.RecordFinishGuess(0, card.ID, "nonfoil"); err != nil {
		t.Fatalf("RecordFinishGuess: %v", err)
	}

	if _, err := s.MoveEntryFinish(cid, card.ID, "nonfoil", "foil", ConditionUnknown); err != nil {
		t.Fatalf("MoveEntryFinish: %v", err)
	}
	rows, err := s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("guesses after the correction = %d, want 0 — the re-key is the evidence", len(rows))
	}
}

// Deleting a container takes its guesses with it (the v26 FK). Before it, a
// removed binder left its guess rows behind forever: `hoard guessed` kept
// listing cards to check in a binder that no longer existed.
func TestDeletingContainerCascadesGuesses(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "cascade-id", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "1", ScryfallURL: "https://scryfall.com/card/c21/1"}
	if err := s.AddCardFinish(card, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	res, err := s.db.Exec(`
INSERT INTO containers (kind, name, source, created_at, updated_at)
VALUES ('collection', 'Trade Binder', 'manual', 'x', 'x')`)
	if err != nil {
		t.Fatalf("creating binder: %v", err)
	}
	bid, _ := res.LastInsertId()
	if err := s.RecordFinishGuess(bid, card.ID, "nonfoil"); err != nil {
		t.Fatalf("RecordFinishGuess: %v", err)
	}

	if _, err := s.db.Exec(`DELETE FROM containers WHERE id = ?`, bid); err != nil {
		t.Fatalf("deleting binder: %v", err)
	}
	rows, err := s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("guesses after deleting their container = %d, want 0", len(rows))
	}
}

// A held row knows a guess is standing against it, which is how the browse
// detail can mark the finish a scan defaulted.
func TestHoldingsCarryTheGuessedFlag(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "levy-id", Name: "Charitable Levy", Set: "mmq",
		CollectorNumber: "12", Finishes: []string{"nonfoil", "foil"},
		ScryfallURL: "https://scryfall.com/card/mmq/12"}
	if err := s.AddCardFinish(card, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.RecordFinishGuess(0, card.ID, "nonfoil"); err != nil {
		t.Fatalf("RecordFinishGuess: %v", err)
	}
	for _, load := range []struct {
		what string
		get  func() ([]Holding, error)
	}{
		{"HoldingsOf", func() ([]Holding, error) { return s.HoldingsOf(card.ID) }},
		{"HoldingsOfName", func() ([]Holding, error) { return s.HoldingsOfName(card.Name) }},
	} {
		hs, err := load.get()
		if err != nil {
			t.Fatalf("%s: %v", load.what, err)
		}
		if len(hs) != 1 || !hs[0].Guessed {
			t.Errorf("%s = %+v, want one holding wearing the guessed flag", load.what, hs)
		}
	}
}
