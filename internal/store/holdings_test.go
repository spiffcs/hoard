package store

import (
	"slices"
	"testing"
)

// EntryKeys is one row per (container, printing, finish): the same card
// main and side in one deck collapses to one membership fact with the
// boards' quantities summed, while the binder's copy and each finish stay
// distinct.
func TestEntryKeysDistinct(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "main", Quantity: 1},
			{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "side", Quantity: 2},
			{ScryfallID: "ulamog-id", Finish: "foil", Board: "main", Quantity: 1},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}

	keys, err := s.EntryKeys()
	if err != nil {
		t.Fatalf("EntryKeys: %v", err)
	}
	want := []EntryKey{
		{ContainerID: cid, ScryfallID: "ulamog-id", Finish: "nonfoil", Quantity: 2},
		{ContainerID: deckID, ScryfallID: "ulamog-id", Finish: "nonfoil", Quantity: 3},
		{ContainerID: deckID, ScryfallID: "ulamog-id", Finish: "foil", Quantity: 1},
	}
	if len(keys) != len(want) {
		t.Fatalf("EntryKeys = %+v, want %d distinct facts", keys, len(want))
	}
	for _, w := range want {
		if !slices.Contains(keys, w) {
			t.Errorf("EntryKeys missing %+v in %+v", w, keys)
		}
	}
}
