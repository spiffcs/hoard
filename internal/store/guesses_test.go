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
