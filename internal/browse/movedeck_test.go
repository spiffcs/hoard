package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func typeInto(m Model, text string) Model {
	for _, r := range text {
		m = key(m, string(r))
	}
	return m
}

func parentOf(t *testing.T, m Model, deck string) int64 {
	t.Helper()
	for _, c := range m.containers {
		if c.Name == deck {
			return c.parentID
		}
	}
	t.Fatalf("no container named %q in %q", deck, sidebar(m))
	return 0
}

func folderID(t *testing.T, m Model, name string) int64 {
	t.Helper()
	for _, c := range m.containers {
		if c.Name == name && c.Kind == kindFolder {
			return c.ID
		}
	}
	t.Fatalf("no folder named %q in %q", name, sidebar(m))
	return 0
}

func TestMoveDeckIntoAnExistingFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Loose Deck")

	m = key(m, "m")
	if m.prompt == nil {
		t.Fatal("m on a deck opened no prompt")
	}
	if !strings.Contains(m.prompt.label, "Loose Deck") {
		t.Errorf("prompt label = %q, want it to name the deck", m.prompt.label)
	}

	m = typeInto(m, "duel_decks")
	m = key(m, "enter")

	if m.prompt != nil {
		t.Fatalf("prompt still open: %+v", m.prompt)
	}
	if got, want := parentOf(t, m, "Loose Deck"), folderID(t, m, "duel_decks"); got != want {
		t.Errorf("Loose Deck parent = %d, want duel_decks %d", got, want)
	}
	want := []string{
		allCardsName, store.LooseName,
		"commander", "  Rich Deck",
		"duel_decks", "  Loose Deck", "  Cheap Deck",
	}
	if got := sidebar(m); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sidebar = %q, want %q", got, want)
	}
}

func TestMoveDeckOutOfItsFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Rich Deck")
	if parentOf(t, m, "Rich Deck") == 0 {
		t.Fatal("Rich Deck starts unfiled; the fixture should file it under commander")
	}

	m = key(m, "m")
	m = key(m, "enter")

	if got := parentOf(t, m, "Rich Deck"); got != 0 {
		t.Errorf("Rich Deck parent = %d, want the top level", got)
	}
	for _, c := range m.containers {
		if c.Name == "Rich Deck" && c.depth != 0 {
			t.Errorf("Rich Deck depth = %d, want 0", c.depth)
		}
	}
}

func TestMovingIntoAnUnknownFolderOffersToCreateIt(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Loose Deck")

	m = key(m, "m")
	m = typeInto(m, "modern")
	m = key(m, "enter")

	if m.confirm == nil {
		t.Fatal("an unknown folder name asked nothing, want a create confirm")
	}
	if !strings.Contains(m.confirm.prompt, "modern") {
		t.Errorf("confirm = %q, want it to name the folder", m.confirm.prompt)
	}

	m = key(m, "y")

	if got, want := parentOf(t, m, "Loose Deck"), folderID(t, m, "modern"); got != want {
		t.Errorf("Loose Deck parent = %d, want the new modern folder %d", got, want)
	}
}

func TestDecliningTheCreateLeavesTheDeckAlone(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Loose Deck")

	m = key(m, "m")
	m = typeInto(m, "modern")
	m = key(m, "enter")
	if m.confirm == nil {
		t.Fatal("an unknown folder name asked nothing, want a create confirm to decline")
	}
	m = key(m, "n")

	if got := parentOf(t, m, "Loose Deck"); got != 0 {
		t.Errorf("Loose Deck parent = %d, want it left at the top level", got)
	}
	for _, c := range m.containers {
		if c.Kind == kindFolder && c.Name == "modern" {
			t.Error("declining still created the folder")
		}
	}
}

func TestMoveRefusesAnythingThatIsNotADeck(t *testing.T) {
	for _, name := range []string{allCardsName, store.LooseName, "commander"} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(t, foldersStore())
			m = pickContainer(t, m, name)

			m = key(m, "m")
			if m.prompt != nil {
				t.Fatalf("m on %q opened a move prompt", name)
			}
			if !m.statusErr || m.status == "" {
				t.Errorf("status = %q (err=%v), want an explanation", m.status, m.statusErr)
			}
		})
	}
}

func TestSidebarHelpAdvertisesMoveOnADeck(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Loose Deck")
	if help := m.helpLine(); !strings.Contains(help, "m move to folder") {
		t.Errorf("sidebar help = %q, want it to advertise m", help)
	}
}

func TestPaletteCreatesAFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = key(m, ":")
	if m.palette == nil {
		t.Fatal("':' did not open the palette")
	}
	m = typeInto(m, "folder")
	if len(m.palette.matches) == 0 {
		t.Fatal("query 'folder' matched nothing")
	}
	if top := m.commands[m.palette.matches[0].index]; top.id != "folder.new" {
		t.Fatalf("top match for 'folder' = %s, want folder.new", top.id)
	}

	m = key(m, "enter")
	if m.prompt == nil {
		t.Fatal("NewFolder opened no prompt")
	}
	m = typeInto(m, "modern")
	m = key(m, "enter")

	var found bool
	for _, c := range m.containers {
		if c.Kind == kindFolder && c.Name == "modern" {
			found = true
		}
	}
	if !found {
		t.Errorf("sidebar = %q, want a modern folder", sidebar(m))
	}
}

func TestNoBinderCommandClaimsTheWordFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())
	seen := 0
	for i := range m.commands {
		if !strings.HasPrefix(m.commands[i].id, "binder.") {
			continue
		}
		seen++
		if strings.Contains(m.commands[i].aliases, "folder") {
			t.Errorf("%s aliases = %q, want no folder: folders are their own thing now",
				m.commands[i].id, m.commands[i].aliases)
		}
	}
	if seen == 0 {
		t.Fatal("no binder commands found; this test no longer proves anything")
	}
}
