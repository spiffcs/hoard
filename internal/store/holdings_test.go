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

// The treated-product mapping follows the vendor-link convention: NULL is
// never-asked, empty is asked-and-none, and only genuine ids come back as
// ids — the resolve gate must be able to tell all three apart.
func TestTCGAltProductsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	ids, stamped, err := s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if len(ids) != 0 || stamped["ulamog-id"] {
		t.Fatalf("fresh card = ids %v stamped %v, want never-asked", ids, stamped)
	}

	if err := s.SaveTCGAltProducts(map[string]string{"ulamog-id": "553005"}); err != nil {
		t.Fatalf("SaveTCGAltProducts: %v", err)
	}
	ids, stamped, err = s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if ids["ulamog-id"] != "553005" || !stamped["ulamog-id"] {
		t.Errorf("ids = %v stamped %v, want the product recorded", ids, stamped)
	}

	// Recorded absence: stamped, but no id.
	if err := s.SaveTCGAltProducts(map[string]string{"ulamog-id": ""}); err != nil {
		t.Fatalf("SaveTCGAltProducts(empty): %v", err)
	}
	ids, stamped, err = s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if len(ids) != 0 || !stamped["ulamog-id"] {
		t.Errorf("ids = %v stamped %v, want asked-and-none", ids, stamped)
	}
}

// Settings round-trip: upserts stick, unknown keys read as absent, and
// re-saving overwrites without touching neighbors.
func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveSettings(map[string]string{"movers.pennies": "true", "market.floor": "0.25"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := s.SaveSettings(map[string]string{"market.floor": "2"}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := s.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got["movers.pennies"] != "true" || got["market.floor"] != "2" {
		t.Errorf("settings = %v, want the upsert to stick and the neighbor to survive", got)
	}
	if _, ok := got["never.set"]; ok {
		t.Error("an unset key must read as absent")
	}
}
