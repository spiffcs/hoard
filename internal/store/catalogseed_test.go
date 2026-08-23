package store

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func catalogPrintings() []CatalogPrinting {
	return []CatalogPrinting{
		{
			Card:     withRaw(ulamog(), `{"rarity":"mythic","set_name":"Ultimate Masters"}`),
			Finishes: []finish.Finish{finish.Nonfoil, finish.Foil},
		},
		{
			Card:     withRaw(solRing(), `{"rarity":"uncommon","set_name":"Commander 2021"}`),
			Finishes: []finish.Finish{finish.Nonfoil},
		},
		{
			Card:     withRaw(etchedOnly(), `{"rarity":"rare","set_name":"Modern Horizons 2"}`),
			Finishes: []finish.Finish{finish.Etched},
		},
	}
}

func withRaw(c scryfall.Card, raw string) scryfall.Card {
	c.Raw = json.RawMessage(raw)
	return c
}

func etchedOnly() scryfall.Card {
	return scryfall.Card{
		ID:              "etched-id",
		Set:             "mh2",
		CollectorNumber: "422",
		Name:            "Ragavan, Nimble Pilferer",
		PriceUSDEtched:  f(60.00),
		ScryfallURL:     "https://scryfall.com/card/mh2/422",
	}
}

func TestSeedCatalogPrintingsMakesEveryPrintingActive(t *testing.T) {
	s := newTestStore(t)
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}

	printings, entries, err := s.SeedCatalogPrintings(cid, catalogPrintings())
	if err != nil {
		t.Fatalf("SeedCatalogPrintings: %v", err)
	}
	if printings != 3 || entries != 4 {
		t.Fatalf("seeded %d printings and %d entries, want 3 and 4 (ulamog prints in two finishes)",
			printings, entries)
	}

	active, err := s.ActivePrintingIDs()
	if err != nil {
		t.Fatalf("ActivePrintingIDs: %v", err)
	}
	slices.Sort(active)
	if want := []string{"etched-id", "sol-id", "ulamog-id"}; !slices.Equal(active, want) {
		t.Errorf("ActivePrintingIDs = %v, want %v — the backfill prices exactly these", active, want)
	}

	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	got := map[string]int{}
	for _, o := range owned {
		got[o.ScryfallID+"/"+o.Finish.String()] = o.Copies
	}
	want := map[string]int{
		"ulamog-id/nonfoil": 1, "ulamog-id/foil": 1,
		"sol-id/nonfoil": 1, "etched-id/etched": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("OwnedByFinish = %v, want one row per printed finish: %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("OwnedByFinish[%s] = %d copies, want %d", k, got[k], v)
		}
	}

	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("AllByFinish returned %d rows, want 4", len(rows))
	}
	var order []string
	for _, r := range rows {
		order = append(order, r.Name)
	}
	if rows[0].Name != "Ragavan, Nimble Pilferer" || rows[len(rows)-1].Name != "Sol Ring" {
		t.Errorf("AllByFinish order = %v, want most expensive printing first and Sol Ring last",
			order)
	}

	var rarity, setName string
	if err := s.db.QueryRow(
		`SELECT rarity, set_name FROM cards WHERE scryfall_id = 'ulamog-id'`).
		Scan(&rarity, &setName); err != nil {
		t.Fatalf("reading generated columns: %v", err)
	}
	if rarity != "mythic" || setName != "Ultimate Masters" {
		t.Errorf("generated columns = %q/%q, want mythic/Ultimate Masters — raw_json was not stored",
			rarity, setName)
	}
}
