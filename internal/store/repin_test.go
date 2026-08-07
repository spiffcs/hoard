package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// RepointDeckPrintings moves a deck's entries to the mapped printings,
// merging with rows the target already has, and leaves other containers'
// entries alone.
func TestRepointDeckPrintings(t *testing.T) {
	s := newTestStore(t)
	wrong := scryfall.Card{ID: "we-hob", Set: "hob", CollectorNumber: "142",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	right := scryfall.Card{ID: "we-cma", Set: "cma", CollectorNumber: "154",
		Name: "Wood Elves", ScryfallURL: "http://x"}

	if err := s.UpsertPrintings([]scryfall.Card{wrong, right}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Guided", Source: "text", SourceID: "guided"},
		[]Entry{{ScryfallID: "we-hob", Finish: "nonfoil", Board: "main", Quantity: 1}})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	// The same wrong printing loose in the binder must not move.
	if err := s.AddCardFinish(wrong, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	moved, err := s.RepointDeckPrintings(deckID, map[string]string{"we-hob": "we-cma"})
	if err != nil {
		t.Fatalf("RepointDeckPrintings: %v", err)
	}
	if moved != 1 {
		t.Errorf("moved = %d, want 1", moved)
	}
	entries, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Card.ScryfallID != "we-cma" || entries[0].Card.SetCode != "cma" {
		t.Fatalf("deck entries = %+v, want the cma printing", entries)
	}
	if held := heldByFinish(t, s, "we-hob"); held["nonfoil"] != 2 {
		t.Errorf("binder copies = %v, want the loose holding untouched", held)
	}

	// Re-pointing onto a printing the deck already holds merges quantities.
	if _, err := s.UpsertDeck(DeckMeta{Name: "Merge", Source: "text", SourceID: "merge"},
		[]Entry{
			{ScryfallID: "we-hob", Finish: "nonfoil", Board: "main", Quantity: 2},
			{ScryfallID: "we-cma", Finish: "nonfoil", Board: "main", Quantity: 1},
		}); err != nil {
		t.Fatalf("UpsertDeck merge fixture: %v", err)
	}
	mergeDeck, err := s.DeckByRef("Merge")
	if err != nil {
		t.Fatalf("DeckByRef: %v", err)
	}
	if _, err := s.RepointDeckPrintings(mergeDeck.ID, map[string]string{"we-hob": "we-cma"}); err != nil {
		t.Fatalf("RepointDeckPrintings merge: %v", err)
	}
	entries, err = s.DeckEntries(mergeDeck.ID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Quantity != 3 {
		t.Fatalf("merged entries = %+v, want one row of 3", entries)
	}
}

// MoveEntry re-points one binder row at another printing or binder,
// merging quantities, and refuses a row that does not exist.
func TestMoveEntry(t *testing.T) {
	s := newTestStore(t)
	wrong := scryfall.Card{ID: "k-mb2", Set: "mb2", CollectorNumber: "148",
		Name: "Kataki, War's Wage", ScryfallURL: "http://x"}
	right := scryfall.Card{ID: "k-md1", Set: "md1", CollectorNumber: "24",
		Name: "Kataki, War's Wage", ScryfallURL: "http://x"}
	if err := s.UpsertPrintings([]scryfall.Card{wrong, right}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := s.AddCardFinish(wrong, "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	binder, err := s.CreateBinder("Trades")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(binder, right, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	binders, err := s.ListBinders()
	if err != nil || len(binders) == 0 {
		t.Fatalf("ListBinders: %v (%d)", err, len(binders))
	}
	collectionID := binders[0].ID // the default binder leads

	// Set change in place: the collection's mb2 row becomes md1.
	prev, err := s.MoveEntry(collectionID, "k-mb2", "nonfoil", ConditionUnknown, collectionID, "k-md1")
	if err != nil {
		t.Fatalf("MoveEntry: %v", err)
	}
	if prev != 0 {
		t.Errorf("prev = %d, want no pre-existing md1 row in the collection", prev)
	}
	if held := heldByFinish(t, s, "k-md1"); held["nonfoil"] != 3 {
		t.Errorf("collection md1 = %v, want the 3 moved copies", held)
	}
	if held := heldByFinish(t, s, "k-mb2"); held["nonfoil"] != 0 {
		t.Errorf("collection mb2 = %v, want gone", held)
	}

	// Container move with merge: 3 collection copies join the binder's 2.
	prev, err = s.MoveEntry(collectionID, "k-md1", "nonfoil", ConditionUnknown, binder, "k-md1")
	if err != nil {
		t.Fatalf("MoveEntry to binder: %v", err)
	}
	if prev != 2 {
		t.Errorf("prev = %d, want the binder's existing 2", prev)
	}
	rows, err := s.BinderByFinish(binder)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 5 {
		t.Fatalf("binder rows = %+v, want one merged row of 5", rows)
	}

	// A row that does not exist refuses rather than inventing one.
	if _, err := s.MoveEntry(collectionID, "k-mb2", "nonfoil", ConditionUnknown, binder, "k-md1"); err == nil {
		t.Error("moving a missing holding must refuse")
	}
}

// ActivePrintingIDs is the refresh set: held or watched printings only —
// the orphans a repin leaves behind must not spend network on every run.
func TestActivePrintingIDs(t *testing.T) {
	s := newTestStore(t)
	held := scryfall.Card{ID: "held-1", Set: "uma", CollectorNumber: "1",
		Name: "Held", ScryfallURL: "http://x"}
	orphan := scryfall.Card{ID: "orphan-1", Set: "hob", CollectorNumber: "2",
		Name: "Orphan", ScryfallURL: "http://x"}
	watched := scryfall.Card{ID: "watched-1", Set: "mh3", CollectorNumber: "3",
		Name: "Watched", ScryfallURL: "http://x"}
	if err := s.UpsertPrintings([]scryfall.Card{held, orphan, watched}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := s.AddCardFinish(held, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("watched-1", "Watched", "nonfoil", "under", 5); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	ids, err := s.ActivePrintingIDs()
	if err != nil {
		t.Fatalf("ActivePrintingIDs: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["held-1"] || !got["watched-1"] || got["orphan-1"] || len(ids) != 2 {
		t.Errorf("ids = %v, want the held and watched printings only", ids)
	}
}

// MoveEntryFinish re-keys a holding's finish in place, merging with copies
// already held in the target finish, and refuses a missing source row.
func TestMoveEntryFinish(t *testing.T) {
	s := newTestStore(t)
	card := scryfall.Card{ID: "bb-uma", Set: "uma", CollectorNumber: "85",
		Name: "Bitterblossom", ScryfallURL: "http://x"}
	if err := s.UpsertPrintings([]scryfall.Card{card}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := s.AddCardFinish(card, "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(card, "foil", 2); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}
	binders, err := s.ListBinders()
	if err != nil || len(binders) == 0 {
		t.Fatalf("ListBinders: %v", err)
	}
	cid := binders[0].ID

	// Re-key with merge: 3 nonfoil join the 2 foil.
	prev, err := s.MoveEntryFinish(cid, "bb-uma", "nonfoil", "foil", ConditionUnknown)
	if err != nil {
		t.Fatalf("MoveEntryFinish: %v", err)
	}
	if prev != 2 {
		t.Errorf("prev = %d, want the existing 2 foils", prev)
	}
	held := heldByFinish(t, s, "bb-uma")
	if held["foil"] != 5 || held["nonfoil"] != 0 {
		t.Errorf("held = %v, want 5 foil and no nonfoil", held)
	}

	// Re-key without a target row: the row simply changes key.
	if _, err := s.MoveEntryFinish(cid, "bb-uma", "foil", "etched", ConditionUnknown); err != nil {
		t.Fatalf("MoveEntryFinish to etched: %v", err)
	}
	if held := heldByFinish(t, s, "bb-uma"); held["etched"] != 5 {
		t.Errorf("held = %v, want 5 etched", held)
	}

	// A missing source refuses.
	if _, err := s.MoveEntryFinish(cid, "bb-uma", "nonfoil", "foil", ConditionUnknown); err == nil {
		t.Error("moving a missing finish row succeeded")
	}
}
