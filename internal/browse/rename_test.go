package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func TestRenameADeckFromTheSidebar(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Loose Deck")

	m = key(m, "R")
	if m.prompt == nil {
		t.Fatalf("R on a deck opened no prompt · status = %q", m.status)
	}
	if !strings.Contains(m.prompt.label, "deck") {
		t.Errorf("prompt label = %q, want it to say deck", m.prompt.label)
	}

	m = key(m, "ctrl+u")
	m = typeInto(m, "Landfall")
	m = key(m, "enter")

	var found bool
	for _, c := range m.containers {
		if c.Name == "Landfall" && c.Kind == store.KindDeck {
			found = true
		}
		if c.Name == "Loose Deck" {
			t.Errorf("old name still listed: %q", sidebar(m))
		}
	}
	if !found {
		t.Errorf("sidebar = %q, want the deck renamed", sidebar(m))
	}
}

func TestRenameAFolderFromTheSidebar(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "commander")

	m = key(m, "R")
	if m.prompt == nil {
		t.Fatalf("R on a folder opened no prompt · status = %q", m.status)
	}
	if strings.Contains(m.status, "imported list") {
		t.Errorf("status = %q, want no talk of imported lists: a folder has no import", m.status)
	}
	if !strings.Contains(m.prompt.label, "folder") {
		t.Errorf("prompt label = %q, want it to say folder", m.prompt.label)
	}

	m = key(m, "ctrl+u")
	m = typeInto(m, "edh")
	m = key(m, "enter")

	var found bool
	for _, c := range m.containers {
		if c.Name == "edh" && c.Kind == kindFolder {
			found = true
		}
	}
	if !found {
		t.Errorf("sidebar = %q, want the folder renamed to edh", sidebar(m))
	}
}

func TestRenameStillRefusesAllCardsAndSets(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, allCardsName)
	m = key(m, "R")
	if m.prompt != nil {
		t.Error("R on All Cards opened a rename prompt")
	}
	if !m.statusErr {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}
