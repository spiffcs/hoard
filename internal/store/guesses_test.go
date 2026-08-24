package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestFinishGuessRoundTrip(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "brainsurge-id", Name: "Brainsurge", Set: "mh3",
		CollectorNumber: "399", ScryfallURL: "https://scryfall.com/card/mh3/399"}
	if err := s.AddCardFinish(card, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}
	for range 2 {
		if err := s.RecordFinishGuess(cid, card.ID, finish.Nonfoil); err != nil {
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
	if rows[0].Name != "Brainsurge" || rows[0].Finish != finish.Nonfoil {
		t.Errorf("row = %+v, want the card joined in", rows[0])
	}

	if err := s.ClearFinishGuess(cid, card.ID, finish.Nonfoil); err != nil {
		t.Fatalf("ClearFinishGuess: %v", err)
	}
	rows, err = s.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("guesses after one clear = %d, want 1", len(rows))
	}

	if err := s.ClearFinishGuess(cid, card.ID, finish.Foil); err != nil {
		t.Errorf("ClearFinishGuess of nothing: %v", err)
	}
}

func TestConfirmFinishGuessRetiresExactlyTheNamedRow(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "whisperer", Name: "Primal Whisperer", Set: "lgn",
		CollectorNumber: "135", ScryfallURL: "https://scryfall.com/card/lgn/135"}
	if err := s.AddCardFinish(card, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}

	for range 2 {
		if err := s.RecordFinishGuess(cid, card.ID, finish.Nonfoil); err != nil {
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

	if _, err := s.ConfirmFinishGuess(rows[1].ID); err != nil {
		t.Fatalf("ConfirmFinishGuess: %v", err)
	}
	if left, _ := s.GuessedFinishes(); len(left) != 0 {
		t.Errorf("remaining = %+v, want an empty queue", left)
	}

	ok, err = s.ConfirmFinishGuess(rows[0].ID)
	if err != nil {
		t.Errorf("ConfirmFinishGuess of nothing: %v", err)
	}
	if ok {
		t.Errorf("ConfirmFinishGuess of an already-retired id reported a retirement")
	}
}

func TestMoveEntryFinishClearsTheGuess(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "digger-id", Name: "Trap Digger", Set: "mmq",
		CollectorNumber: "50", Finishes: []string{"nonfoil", "foil"},
		ScryfallURL: "https://scryfall.com/card/mmq/50"}
	if err := s.AddCardFinish(card, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}

	if err := s.RecordFinishGuess(0, card.ID, finish.Nonfoil); err != nil {
		t.Fatalf("RecordFinishGuess: %v", err)
	}

	if _, err := s.MoveEntryFinish(mainRef(cid, card.ID, finish.Nonfoil, ConditionUnknown), finish.Foil); err != nil {
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

func TestDeletingContainerCascadesGuesses(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "cascade-id", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "1", ScryfallURL: "https://scryfall.com/card/c21/1"}
	if err := s.AddCardFinish(card, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	res, err := s.db.Exec(`
INSERT INTO containers (kind, name, source, created_at, updated_at)
VALUES ('collection', 'Trade Binder', 'manual', 'x', 'x')`)
	if err != nil {
		t.Fatalf("creating binder: %v", err)
	}
	bid, _ := res.LastInsertId()
	if err := s.RecordFinishGuess(bid, card.ID, finish.Nonfoil); err != nil {
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

func TestHoldingsCarryTheGuessedFlag(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "levy-id", Name: "Charitable Levy", Set: "mmq",
		CollectorNumber: "12", Finishes: []string{"nonfoil", "foil"},
		ScryfallURL: "https://scryfall.com/card/mmq/12"}
	if err := s.AddCardFinish(card, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.RecordFinishGuess(0, card.ID, finish.Nonfoil); err != nil {
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
