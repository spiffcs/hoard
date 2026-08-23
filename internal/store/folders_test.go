package store

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func folderStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "Atraxa", Source: "text", SourceID: "atraxa"},
		[]Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1}},
	); err != nil {
		t.Fatalf("UpsertDeck Atraxa: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "Kozilek", Source: "text", SourceID: "kozilek"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 2}},
	); err != nil {
		t.Fatalf("UpsertDeck Kozilek: %v", err)
	}
	return s
}

func mustFolder(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id, err := s.CreateFolder(name)
	if err != nil {
		t.Fatalf("CreateFolder %q: %v", name, err)
	}
	if id == 0 {
		t.Fatalf("CreateFolder %q returned id 0", name)
	}
	return id
}

func inFolder(t *testing.T, s *Store, deck string, folderID int64) {
	t.Helper()
	if got := deckNamed(t, s, deck).ParentID; got != folderID {
		t.Fatalf("%s parent = %d, want folder %d", deck, got, folderID)
	}
}

func deckNamed(t *testing.T, s *Store, name string) DeckSummary {
	t.Helper()
	decks, err := s.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	for _, d := range decks {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no deck %q in %+v", name, decks)
	return DeckSummary{}
}

func TestMoveDeckIntoAFolder(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	atraxa := deckNamed(t, s, "Atraxa")
	if err := s.MoveDeckToFolder(atraxa.ID, fid); err != nil {
		t.Fatalf("MoveDeckToFolder: %v", err)
	}

	if got := deckNamed(t, s, "Atraxa").ParentID; got != fid {
		t.Errorf("Atraxa parent = %d, want folder %d", got, fid)
	}
	if got := deckNamed(t, s, "Kozilek").ParentID; got != 0 {
		t.Errorf("Kozilek parent = %d, want the root", got)
	}

	if err := s.MoveDeckToFolder(atraxa.ID, 0); err != nil {
		t.Fatalf("MoveDeckToFolder root: %v", err)
	}
	if got := deckNamed(t, s, "Atraxa").ParentID; got != 0 {
		t.Errorf("Atraxa parent = %d after moving to root, want 0", got)
	}
}

func TestFoldersHoldOnlyDecks(t *testing.T) {
	s := folderStore(t)
	commander := mustFolder(t, s, "commander")
	duel := mustFolder(t, s, "duel_decks")
	binderID, err := s.CreateBinder("Trade Stock")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	atraxa := deckNamed(t, s, "Atraxa")

	if err := s.MoveDeckToFolder(binderID, commander); err == nil {
		t.Error("moving a binder into a folder succeeded, want a refusal")
	}
	if err := s.MoveDeckToFolder(duel, commander); err == nil {
		t.Error("nesting a folder succeeded, want a refusal: folders are one level")
	}
	if err := s.MoveDeckToFolder(atraxa.ID, atraxa.ID); err == nil {
		t.Error("moving a deck into a deck succeeded, want a refusal")
	}
	if err := s.MoveDeckToFolder(atraxa.ID, 9999); err == nil {
		t.Error("moving into a missing folder succeeded, want a refusal")
	}
}

func TestRemovingAFolderKeepsItsDecksAndTheirCards(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	atraxa := deckNamed(t, s, "Atraxa")
	if err := s.MoveDeckToFolder(atraxa.ID, fid); err != nil {
		t.Fatalf("MoveDeckToFolder: %v", err)
	}
	inFolder(t, s, "Atraxa", fid)

	if err := s.RemoveFolder(fid); err != nil {
		t.Fatalf("RemoveFolder: %v", err)
	}

	back := deckNamed(t, s, "Atraxa")
	if back.ParentID != 0 {
		t.Errorf("Atraxa parent = %d after its folder went, want the root", back.ParentID)
	}
	entries, err := s.DeckEntries(atraxa.ID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Quantity != 1 {
		t.Fatalf("Atraxa entries = %+v, want its Sol Ring still there", entries)
	}
	folders, err := s.ListFolders()
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("folders = %+v, want none left", folders)
	}
}

func TestAFolderHoldsNoCards(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	if err := s.AddCardFinishTo(fid, solRing(), finish.Nonfoil, 1); err == nil {
		t.Error("adding a card to a folder succeeded, want a refusal")
	}
	if _, err := s.SetHoldingQuantityIn(fid, "sol-id", finish.Nonfoil, "unknown", 3); err == nil {
		t.Error("setting a holding in a folder succeeded, want a refusal")
	}
}

func TestListFoldersRollsUpItsDecks(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	for _, name := range []string{"Atraxa", "Kozilek"} {
		if err := s.MoveDeckToFolder(deckNamed(t, s, name).ID, fid); err != nil {
			t.Fatalf("MoveDeckToFolder %s: %v", name, err)
		}
	}

	folders, err := s.ListFolders()
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("folders = %+v, want one", folders)
	}
	f := folders[0]
	if f.Name != "commander" || f.Kind != KindFolder {
		t.Errorf("folder = %+v, want commander/%s", f, KindFolder)
	}
	if f.TotalCopies != 3 {
		t.Errorf("copies = %d, want 3 (1 Sol Ring + 2 Ulamog)", f.TotalCopies)
	}
	if f.DistinctCards != 2 {
		t.Errorf("distinct = %d, want 2", f.DistinctCards)
	}
	if f.Value != 22 {
		t.Errorf("value = %v, want 22 (2.00 + 2x10.00)", f.Value)
	}
}

func TestFolderByFinishMergesEveryDecksCards(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	for _, name := range []string{"Atraxa", "Kozilek"} {
		if err := s.MoveDeckToFolder(deckNamed(t, s, name).ID, fid); err != nil {
			t.Fatalf("MoveDeckToFolder %s: %v", name, err)
		}
	}

	rows, err := s.FolderByFinish(fid)
	if err != nil {
		t.Fatalf("FolderByFinish: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.ScryfallID] += r.Quantity
	}
	if len(got) != 2 || got["sol-id"] != 1 || got["ulamog-id"] != 2 {
		t.Errorf("folder rows = %v, want 1 Sol Ring and 2 Ulamog", got)
	}
}

func TestFolderNamesAreCheckedLikeBinderNames(t *testing.T) {
	s := folderStore(t)
	if _, err := s.CreateFolder("  "); err == nil {
		t.Error("blank folder name accepted, want a refusal")
	}
	if _, err := s.CreateFolder("commander"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := s.CreateFolder("Commander"); err == nil {
		t.Error("duplicate folder name accepted, want a refusal")
	}
	fid, err := s.CreateFolder("duel")
	if err != nil {
		t.Fatalf("CreateFolder duel: %v", err)
	}
	if err := s.RenameFolder(fid, "duel_decks"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	got, err := s.FolderByRef("duel_decks")
	if err != nil {
		t.Fatalf("FolderByRef: %v", err)
	}
	if got == nil || got.Name != "duel_decks" || got.Kind != KindFolder {
		t.Errorf("FolderByRef = %+v, want the renamed folder", got)
	}
	if err := s.RenameFolder(fid, "commander"); err == nil ||
		!strings.Contains(err.Error(), "commander") {
		t.Errorf("renaming onto a taken name = %v, want a refusal naming it", err)
	}
}
