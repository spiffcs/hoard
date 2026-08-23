package browse

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func folder(id int64, name string, copies int, value float64) store.DeckSummary {
	d := store.DeckSummary{DistinctCards: copies, TotalCopies: copies, Value: value}
	d.ID = id
	d.Name = name
	d.Kind = store.KindFolder
	return d
}

func inFolder(d store.DeckSummary, folderID int64) store.DeckSummary {
	d.ParentID = folderID
	return d
}

func foldersStore() *fakeStore {
	f := testStore()
	f.folders = []store.DeckSummary{
		folder(301, "commander", 100, 500),
		folder(302, "duel_decks", 100, 50),
	}
	f.decks = []store.DeckSummary{
		inFolder(deck(201, "Cheap Deck", 100, 50), 302),
		inFolder(deck(202, "Rich Deck", 100, 500), 301),
		deck(203, "Loose Deck", 100, 200),
	}
	f.folderRows = map[int64][]store.CollectionRow{
		301: {
			row("Bitterblossom", "uma", "85", finish.Nonfoil, 4, 136),
			row("Sol Ring", "c21", "1", finish.Nonfoil, 1, 10),
		},
	}
	return f
}

func sidebar(m Model) []string {
	var out []string
	for _, c := range m.containers {
		prefix := ""
		for range c.depth {
			prefix += "  "
		}
		out = append(out, prefix+c.Name)
	}
	return out
}

func TestSidebarNestsDecksUnderTheirFolder(t *testing.T) {
	m := newTestModel(t, foldersStore())

	want := []string{
		allCardsName,
		store.LooseName,
		"commander",
		"  Rich Deck",
		"Loose Deck",
		"duel_decks",
		"  Cheap Deck",
	}
	got := sidebar(m)
	if len(got) != len(want) {
		t.Fatalf("sidebar = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sidebar = %q, want %q", got, want)
		}
	}
}

func TestFolderRowRollsUpItsDecks(t *testing.T) {
	m := newTestModel(t, foldersStore())

	var f *container
	for i := range m.containers {
		if m.containers[i].Name == "commander" {
			f = &m.containers[i]
		}
	}
	if f == nil {
		t.Fatalf("no commander folder in %q", sidebar(m))
	}
	if f.Kind != kindFolder {
		t.Errorf("kind = %q, want %q", f.Kind, kindFolder)
	}
	if f.Copies != 100 || f.Value != 500 {
		t.Errorf("commander = %d copies / %v, want its one deck's 100 / 500", f.Copies, f.Value)
	}
	if f.parentID != 0 || f.depth != 0 {
		t.Errorf("folder parent/depth = %d/%d, want a top-level row", f.parentID, f.depth)
	}

	var all *container
	for i := range m.containers {
		if m.containers[i].Kind == kindAllCards {
			all = &m.containers[i]
		}
	}
	if all.Copies != 8+100+100+100 {
		t.Errorf("All Cards copies = %d, want the binder and three decks counted once each", all.Copies)
	}
}

func TestSelectingAFolderShowsEveryDecksCards(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "commander")

	if sel := m.selectedContainer(); sel == nil || sel.Kind != kindFolder {
		t.Fatalf("selected %+v, want the commander folder", sel)
	}
	got := map[string]int{}
	for _, c := range m.cards {
		got[c.Name] += c.Quantity
	}
	if len(got) != 2 || got["Bitterblossom"] != 4 || got["Sol Ring"] != 1 {
		t.Errorf("folder cards = %v, want its decks' cards merged", got)
	}
	if title, _ := m.viewHeader(); title != "CARDS · COMMANDER" {
		t.Errorf("header = %q, want the folder named", title)
	}
}

func TestFolderIsNotOfferedAsACardDestination(t *testing.T) {
	m := newTestModel(t, foldersStore())
	m = pickContainer(t, m, "commander")

	if ok, _ := m.editable(); ok {
		t.Error("a folder reports as editable, want cards refused: a folder holds decks")
	}
}
