package store

import (
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"testing"
)

func TestEntryKeysDistinct(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "side", Quantity: 2},
			{ScryfallID: "ulamog-id", Finish: finish.Foil, Board: "main", Quantity: 1},
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
		{ContainerID: cid, ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Quantity: 2},
		{ContainerID: deckID, ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Quantity: 3},
		{ContainerID: deckID, ScryfallID: "ulamog-id", Finish: finish.Foil, Quantity: 1},
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

func TestTCGAltProductsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	ids, _, stamped, err := s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if len(ids) != 0 || stamped["ulamog-id"] {
		t.Fatalf("fresh card = ids %v stamped %v, want never-asked", ids, stamped)
	}

	if err := s.SaveTCGAltProducts(map[string]string{"ulamog-id": "553005"}, nil); err != nil {
		t.Fatalf("SaveTCGAltProducts: %v", err)
	}
	ids, _, stamped, err = s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if ids["ulamog-id"] != "553005" || !stamped["ulamog-id"] {
		t.Errorf("ids = %v stamped %v, want the product recorded", ids, stamped)
	}

	if err := s.SaveTCGAltProducts(map[string]string{"ulamog-id": ""}, nil); err != nil {
		t.Fatalf("SaveTCGAltProducts(empty): %v", err)
	}
	ids, _, stamped, err = s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if len(ids) != 0 || !stamped["ulamog-id"] {
		t.Errorf("ids = %v stamped %v, want asked-and-none", ids, stamped)
	}
}

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
