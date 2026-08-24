package browse

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/store"
)

func sidebarRow(t *testing.T, m Model, name string) string {
	t.Helper()
	for _, l := range m.containerLines(40) {
		if strings.Contains(l, name) {
			return strings.TrimRight(l, " ")
		}
	}
	t.Fatalf("no rendered row for %q in %q", name, m.containerLines(40))
	return ""
}

func TestSpaceFoldsAFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "commander")

	m = key(m, " ")

	want := []string{
		allCardsName, store.LooseName,
		"commander",
		"Loose Deck",
		"duel_decks", "  Cheap Deck",
	}
	if got := sidebar(m); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sidebar = %q, want Rich Deck folded away: %q", got, want)
	}
	for _, c := range m.containers {
		if c.Name != "commander" {
			continue
		}
		if c.Copies != 100 || c.Value != 500 {
			t.Errorf("folded commander = %d copies / %v, want its roll-up kept", c.Copies, c.Value)
		}
	}
}

func TestSpaceUnfoldsAgain(t *testing.T) {
	m := newTestModel(t, foldersStore())
	before := strings.Join(sidebar(m), "|")

	m = pickContainer(t, m, "commander")
	m = key(m, " ")
	if strings.Join(sidebar(m), "|") == before {
		t.Fatal("first space changed nothing")
	}

	m = key(m, " ")
	if got := strings.Join(sidebar(m), "|"); got != before {
		t.Errorf("sidebar = %q, want the original %q", got, before)
	}
}

func TestSpaceOnADeckFoldsItsParent(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "Rich Deck")

	m = key(m, " ")

	sel := m.selectedContainer()
	if sel == nil || sel.Name != "commander" {
		t.Fatalf("selected %+v, want the cursor moved to the folder it folded", sel)
	}
	for _, c := range m.containers {
		if c.Name == "Rich Deck" {
			t.Fatalf("Rich Deck is still listed: %q", sidebar(m))
		}
	}
	got := map[string]int{}
	for _, c := range m.cards {
		got[c.Name] += c.Quantity
	}
	if len(got) != 2 {
		t.Errorf("cards = %v, want the folder's cards after the cursor moved", got)
	}
}

func TestFoldedFolderIsMarkedInTheSidebar(t *testing.T) {
	m := newTestModel(t, foldersStore())
	if row := sidebarRow(t, m, "commander"); !strings.HasPrefix(row, foldOpenMark) {
		t.Errorf("row = %q, want it to start with the open marker %q", row, foldOpenMark)
	}

	m = pickContainer(t, m, "commander")
	m = key(m, " ")
	row := sidebarRow(t, m, "commander")
	if !strings.HasPrefix(row, foldShutMark) {
		t.Errorf("row = %q, want it to start with the folded marker %q", row, foldShutMark)
	}
	if strings.Contains(row, foldOpenMark) {
		t.Errorf("row = %q, still shows the open marker", row)
	}
	if plain := sidebarRow(t, m, store.LooseName); strings.ContainsAny(plain, foldOpenMark+foldShutMark) {
		t.Errorf("binder row = %q, want no fold marker: only folders fold", plain)
	}
}

func TestFoldStateSurvivesAReload(t *testing.T) {
	f := foldersStore()
	m := newTestModel(t, f)
	m = pickContainer(t, m, "commander")
	m = key(m, " ")

	if f.settings[setSidebarCollapsed] == "" {
		t.Fatalf("settings = %v, want the fold recorded under %s", f.settings, setSidebarCollapsed)
	}

	next := newTestModel(t, f)
	for _, c := range next.containers {
		if c.Name == "Rich Deck" {
			t.Fatalf("a fresh model unfolded commander: %q", sidebar(next))
		}
	}
}

func TestMovingADeckIntoAFoldedFolderExpandsIt(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "duel_decks")
	m = key(m, " ")
	for _, c := range m.containers {
		if c.Name == "Cheap Deck" {
			t.Fatalf("duel_decks did not fold; nothing to expand: %q", sidebar(m))
		}
	}

	m = pickContainer(t, m, "Loose Deck")
	m = key(m, "m")
	m = typeInto(m, "duel_decks")
	m = key(m, "enter")

	var found bool
	for _, c := range m.containers {
		if c.Name == "Loose Deck" && c.depth == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("sidebar = %q, want the folder opened so the move is visible", sidebar(m))
	}
}

func TestSpaceOutsideAFolderExplainsItself(t *testing.T) {
	for _, name := range []string{allCardsName, store.LooseName, "Loose Deck"} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(t, foldersStore())
			m = pickContainer(t, m, name)
			before := strings.Join(sidebar(m), "|")

			m = key(m, " ")

			if got := strings.Join(sidebar(m), "|"); got != before {
				t.Errorf("sidebar = %q, want it unchanged", got)
			}
			if !m.statusErr || m.status == "" {
				t.Errorf("status = %q (err=%v), want an explanation", m.status, m.statusErr)
			}
		})
	}
}

func TestSidebarHelpAdvertisesFold(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "commander")
	if help := m.helpLine(); !strings.Contains(help, "space fold") {
		t.Errorf("sidebar help = %q, want it to advertise space", help)
	}
}

func longNameStore() *fakeStore {
	f := foldersStore()
	f.decks = []store.DeckSummary{
		inFolder(deck(201, "Cheap Deck", 100, 50), 302),
		inFolder(deck(202, "Eldrazi Incursion Primer", 100, 500), 301),
		deck(203, "Loose Deck", 100, 200),
	}
	return f
}

func valueColumnAt(t *testing.T, m Model) int {
	t.Helper()
	lines := m.containerLines(28)
	if len(lines) == 0 {
		t.Fatal("no sidebar lines")
	}
	at := strings.Index(lines[0], "VALUE")
	if at < 0 {
		t.Fatalf("no VALUE header in %q", lines[0])
	}
	return ansi.StringWidth(lines[0][:at])
}

func TestFoldingDoesNotMoveTheValueColumn(t *testing.T) {
	m := newTestModel(t, longNameStore())
	open := valueColumnAt(t, m)

	m = pickContainer(t, m, "commander")
	m = key(m, " ")
	folded := valueColumnAt(t, m)

	if folded != open {
		t.Errorf("VALUE sits at %d folded and %d open, want it to stay put", folded, open)
	}
}

func TestEveryRowsValueEndsAtTheSameColumn(t *testing.T) {
	m := newTestModel(t, longNameStore())
	right := -1
	for i, l := range m.containerLines(28) {
		if i == 0 {
			continue
		}
		end := ansi.StringWidth(strings.TrimRight(l, " "))
		if right == -1 {
			right = end
			continue
		}
		if end != right {
			t.Errorf("row %d ends at %d, want every value to end at %d: %q", i, end, right, l)
		}
	}
}
