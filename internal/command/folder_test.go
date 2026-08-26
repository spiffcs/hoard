package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func folderCmdStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sol := scryfall.Card{
		ID: "sol-id", Set: "c21", CollectorNumber: "1", Name: "Sol Ring",
		PriceUSD: ptr(2.00), ScryfallURL: "https://scryfall.com/card/c21/1",
		Finishes: []string{"nonfoil", "foil"},
	}
	stubFetch(t, sol)
	if err := st.UpsertPrintings([]scryfall.Card{sol}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := st.UpsertDeck(
		store.DeckMeta{Name: "Atraxa", Source: "text", SourceID: "atraxa"},
		[]store.Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return st
}

func ptr(v float64) *float64 { return &v }

func run(t *testing.T, st *store.Store, args ...string) string {
	t.Helper()
	out, err := execCmd(context.Background(), st, args, false)
	if err != nil {
		t.Fatalf("hoard %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func TestFolderNewThenDeckMoveFilesTheDeck(t *testing.T) {
	st := folderCmdStore(t)

	if out := run(t, st, "folder", "new", "commander"); !strings.Contains(out, "commander") {
		t.Errorf("folder new said %q, want it to name the folder", out)
	}
	if out := run(t, st, "deck", "move", "Atraxa", "commander"); !strings.Contains(out, "commander") {
		t.Errorf("deck move said %q, want it to name the folder", out)
	}

	folders, err := st.ListFolders()
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 || folders[0].Name != "commander" {
		t.Fatalf("folders = %+v, want commander", folders)
	}
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].ParentID != folders[0].ID {
		t.Fatalf("decks = %+v, want Atraxa under folder %d", decks, folders[0].ID)
	}

	out := run(t, st, "folder", "list")
	if !strings.Contains(out, "commander") {
		t.Errorf("folder list = %q, want commander listed", out)
	}
	if strings.Contains(out, "not counted toward your collection") {
		t.Errorf("folder list = %q, want no binder exclusion marker: "+
			"a folder holds decks, which never count toward the collection", out)
	}
}

func TestDeckMoveWithNoFolderReturnsItToTheTopLevel(t *testing.T) {
	st := folderCmdStore(t)
	run(t, st, "folder", "new", "commander")
	run(t, st, "deck", "move", "Atraxa", "commander")

	run(t, st, "deck", "move", "Atraxa")

	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].ParentID != 0 {
		t.Fatalf("decks = %+v, want Atraxa back at the top level", decks)
	}
}

func TestFolderRemoveKeepsTheDecksItHeld(t *testing.T) {
	st := folderCmdStore(t)
	run(t, st, "folder", "new", "commander")
	run(t, st, "deck", "move", "Atraxa", "commander")

	run(t, st, "folder", "rm", "commander")

	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].Name != "Atraxa" || decks[0].ParentID != 0 {
		t.Fatalf("decks = %+v, want Atraxa surviving at the top level", decks)
	}
	if decks[0].TotalCopies != 1 {
		t.Errorf("Atraxa copies = %d, want its Sol Ring intact", decks[0].TotalCopies)
	}
}

func TestDeckMoveRefusesATargetThatIsNotAFolder(t *testing.T) {
	st := folderCmdStore(t)
	if _, err := st.CreateBinder("Trade Stock"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	run(t, st, "folder", "new", "commander")
	run(t, st, "deck", "move", "Atraxa", "commander")

	if _, err := execCmd(context.Background(), st,
		[]string{"deck", "move", "Atraxa", "Trade Stock"}, false); err == nil {
		t.Error("deck move onto a binder succeeded, want a refusal")
	}
	if _, err := execCmd(context.Background(), st,
		[]string{"deck", "move", "Atraxa", "nope"}, false); err == nil {
		t.Error("deck move onto a missing folder succeeded, want a refusal")
	}
}

func TestDeckRenameFromTheCLI(t *testing.T) {
	st := folderCmdStore(t)
	if out := run(t, st, "deck", "rename", "Atraxa", "Atraxa — primer"); !strings.Contains(out, "primer") {
		t.Errorf("deck rename said %q, want it to name the deck", out)
	}
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].Name != "Atraxa — primer" {
		t.Fatalf("decks = %+v, want the renamed deck", decks)
	}
}

func importedDeckStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sol := scryfall.Card{
		ID: "sol-id", Set: "c21", CollectorNumber: "1", Name: "Sol Ring",
		PriceUSD: ptr(2.00), ScryfallURL: "https://scryfall.com/card/c21/1",
		Finishes: []string{"nonfoil", "foil"},
	}
	stubFetch(t, sol)
	if err := st.UpsertPrintings([]scryfall.Card{sol}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	path := deckFile(t, "deck.txt", "1 Sol Ring\n")
	run(t, st, "deck", "add", "--file", path, "--name", "Atraxa", "--source", "text")
	return st, path
}

func deckNames(t *testing.T, st *store.Store) []string {
	t.Helper()
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	var out []string
	for _, d := range decks {
		out = append(out, d.Name)
	}
	return out
}

func TestRefreshingARenamedDeckSaysTheNameWasKept(t *testing.T) {
	st, path := importedDeckStore(t)
	run(t, st, "deck", "rename", "Atraxa", "Atraxa — primer")

	out := run(t, st, "deck", "add", "--file", path, "--name", "Atraxa",
		"--source", "text", "--refresh")

	if !strings.Contains(out, "Atraxa — primer") {
		t.Errorf("refresh said %q, want it to report the name you kept", out)
	}
	if got := deckNames(t, st); len(got) != 1 || got[0] != "Atraxa — primer" {
		t.Fatalf("decks = %q, want the rename kept", got)
	}
}

func TestRenameFromSourceTakesTheImportedNameBack(t *testing.T) {
	st, path := importedDeckStore(t)
	run(t, st, "deck", "rename", "Atraxa", "Atraxa — primer")

	run(t, st, "deck", "add", "--file", path, "--name", "Atraxa",
		"--source", "text", "--refresh", "--rename-from-source")

	if got := deckNames(t, st); len(got) != 1 || got[0] != "Atraxa" {
		t.Fatalf("decks = %q, want the imported name back", got)
	}
}
