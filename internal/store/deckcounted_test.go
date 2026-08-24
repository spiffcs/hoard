package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// loanerStore holds two Ulamogs ($10 each) in the default binder and a
// "Loaner" deck of three Sol Rings ($2 each) that is not counted toward the
// collection — the deck-shaped twin of wantsStore.
func loanerStore(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	loaner, err := s.UpsertDeck(
		DeckMeta{Name: "Loaner", Source: "text", SourceID: "loaner"},
		[]Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 3}})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	if err := s.SetContainerCounted(loaner, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	return s, loaner
}

func TestDeckCountedRoundTrips(t *testing.T) {
	s, loaner := loanerStore(t)

	decks, err := s.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 {
		t.Fatalf("ListDecks = %+v, want the Loaner deck to still be listed", decks)
	}
	if decks[0].Counted {
		t.Errorf("deck %q counted = true, want false after being excluded", decks[0].Name)
	}

	if err := s.SetContainerCounted(loaner, true); err != nil {
		t.Fatalf("SetContainerCounted(true): %v", err)
	}
	decks, _ = s.ListDecks()
	if !decks[0].Counted {
		t.Errorf("deck %q is still uncounted after being switched back", decks[0].Name)
	}
}

func TestUncountedDeckIsLeftOutOfAllCardsAndSets(t *testing.T) {
	s, _ := loanerStore(t)

	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Ulamog, the Infinite Gyre" {
		t.Errorf("AllByFinish = %v, want only the owned card", collNames(rows))
	}

	sets, err := s.SetsHeld()
	if err != nil {
		t.Fatalf("SetsHeld: %v", err)
	}
	var codes []string
	for _, st := range sets {
		codes = append(codes, st.Code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"uma"}) {
		t.Errorf("SetsHeld = %v, want only uma — c21 is on loan, not held", codes)
	}
}

func TestUncountedDeckCardsAreStillPricedButCountZero(t *testing.T) {
	s, _ := loanerStore(t)

	ids, err := s.ActivePrintingIDs()
	if err != nil {
		t.Fatalf("ActivePrintingIDs: %v", err)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"sol-id", "ulamog-id"}) {
		t.Errorf("ActivePrintingIDs = %v, want both — a loaned card still needs prices", ids)
	}

	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	copies := map[string]int{}
	value := map[string]float64{}
	for _, o := range owned {
		copies[o.ScryfallID] = o.Copies
		value[o.ScryfallID] = o.Value
	}
	if len(copies) != 2 {
		t.Fatalf("OwnedByFinish = %v, want both printings so the backfill prices them", copies)
	}
	if copies["ulamog-id"] != 2 {
		t.Errorf("owned copies = %d, want 2", copies["ulamog-id"])
	}
	if copies["sol-id"] != 0 || value["sol-id"] != 0 {
		t.Errorf("loaned card reports %d copies worth %v, want 0 of each",
			copies["sol-id"], value["sol-id"])
	}
}

func TestUncountedDeckIsLeftOutOfCollectionTotalsViaItsFolder(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	for _, name := range []string{"Atraxa", "Kozilek"} {
		if err := s.MoveDeckToFolder(deckNamed(t, s, name).ID, fid); err != nil {
			t.Fatalf("MoveDeckToFolder %s: %v", name, err)
		}
	}
	if err := s.SetContainerCounted(deckNamed(t, s, "Kozilek").ID, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}

	folders, err := s.ListFolders()
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("ListFolders = %+v, want one folder", folders)
	}
	f := folders[0]
	if f.TotalCopies != 1 || f.DistinctCards != 1 {
		t.Errorf("folder holds %d copies of %d cards, want only Atraxa's single Sol Ring",
			f.TotalCopies, f.DistinctCards)
	}
	if f.Value != 2 {
		t.Errorf("folder value = %v, want 2 — the excluded Kozilek must not inflate it", f.Value)
	}
}

func TestAFolderCannotBeLeftOutOfTheCollection(t *testing.T) {
	s := folderStore(t)
	fid := mustFolder(t, s, "commander")
	if err := s.SetContainerCounted(fid, false); err == nil {
		t.Error("SetContainerCounted excluded a folder; only binders and decks hold cards")
	}
}
