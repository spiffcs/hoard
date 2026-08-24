package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func refreshAtraxa(t *testing.T, s *Store, name string, qty int) {
	t.Helper()
	if _, err := s.UpsertDeck(
		DeckMeta{Name: name, Source: "text", SourceID: "atraxa"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: qty}},
	); err != nil {
		t.Fatalf("UpsertDeck refresh: %v", err)
	}
}

func TestRenamingADeckSurvivesARefresh(t *testing.T) {
	s := folderStore(t)
	d := deckNamed(t, s, "Atraxa")
	if err := s.RenameDeck(d.ID, "Atraxa — primer"); err != nil {
		t.Fatalf("RenameDeck: %v", err)
	}
	if got := deckNamed(t, s, "Atraxa — primer").ID; got != d.ID {
		t.Fatalf("renamed deck id = %d, want %d", got, d.ID)
	}

	refreshAtraxa(t, s, "Atraxa", 3)

	after := deckNamed(t, s, "Atraxa — primer")
	if after.ID != d.ID {
		t.Errorf("deck id changed to %d, want %d", after.ID, d.ID)
	}
	if after.TotalCopies != 3 {
		t.Errorf("copies = %d, want the refresh to have updated the cards", after.TotalCopies)
	}
}

func TestADeckYouNeverRenamedTakesTheImportedName(t *testing.T) {
	s := folderStore(t)
	before := deckNamed(t, s, "Atraxa")

	refreshAtraxa(t, s, "Atraxa Infect", 2)

	after := deckNamed(t, s, "Atraxa Infect")
	if after.ID != before.ID {
		t.Errorf("deck id = %d, want the same row %d renamed by its source", after.ID, before.ID)
	}
}

func TestTakingTheImportedNameBack(t *testing.T) {
	s := folderStore(t)
	d := deckNamed(t, s, "Atraxa")
	if err := s.RenameDeck(d.ID, "Atraxa — primer"); err != nil {
		t.Fatalf("RenameDeck: %v", err)
	}
	if got := deckNamed(t, s, "Atraxa — primer").ID; got != d.ID {
		t.Fatalf("rename did not take; nothing to take back")
	}
	if err := s.UnlockDeckName(d.ID); err != nil {
		t.Fatalf("UnlockDeckName: %v", err)
	}

	refreshAtraxa(t, s, "Atraxa", 1)

	if got := deckNamed(t, s, "Atraxa").ID; got != d.ID {
		t.Errorf("deck %d did not take its imported name back", d.ID)
	}
}

func TestRenameDeckRefusesABlankNameAndAMissingDeck(t *testing.T) {
	s := folderStore(t)
	d := deckNamed(t, s, "Atraxa")
	if err := s.RenameDeck(d.ID, "   "); err == nil {
		t.Error("blank deck name accepted, want a refusal")
	}
	if err := s.RenameDeck(9999, "Nope"); err == nil {
		t.Error("renaming a missing deck succeeded, want a refusal")
	}
	binderID, err := s.CreateBinder("Trade Stock")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.RenameDeck(binderID, "Nope"); err == nil {
		t.Error("RenameDeck renamed a binder, want a refusal")
	}
}
