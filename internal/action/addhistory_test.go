package action

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func addedSeries(t *testing.T, d Deps, id string, fin finish.Finish) int {
	t.Helper()
	series, err := d.Store.PriceSeries(id, fin)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	return len(series)
}

func TestAddListRecordsAPriceForWhatItAdded(t *testing.T) {
	d := deckDeps(t, solRing())

	if _, err := AddList(context.Background(), d, nil, AddListOptions{
		Data: []byte("2 Sol Ring (C21) 125\n"), Display: "paste"}); err != nil {
		t.Fatalf("AddList: %v", err)
	}

	if n := addedSeries(t, d, "sol-id-1", finish.Nonfoil); n == 0 {
		t.Error("the list's cards have no price point; their sparklines stay flat " +
			"until the next update-prices")
	}
}

func TestDeckAddRecordsAPriceForWhatItAdded(t *testing.T) {
	d := deckDeps(t, solRing())
	deck := parseDeck(t, "Ramp", "1 Sol Ring\n")

	if _, err := DeckAdd(context.Background(), d, nil, deck, DeckAddOptions{}); err != nil {
		t.Fatalf("DeckAdd: %v", err)
	}

	if n := addedSeries(t, d, "sol-id-1", finish.Nonfoil); n == 0 {
		t.Error("the deck's cards have no price point")
	}
}

func TestImportCollectionRecordsAPriceForWhatItAdded(t *testing.T) {
	d := deckDeps(t, solRing())

	csv := "Name,Set code,Collector number,Quantity,Foil\nSol Ring,c21,125,2,normal\n"
	if _, err := ImportCollection(context.Background(), d, nil, ImportOptions{
		Data: []byte(csv), Display: "manabox.csv", Format: "manabox"}); err != nil {
		t.Fatalf("ImportCollection: %v", err)
	}

	if n := addedSeries(t, d, "sol-id-1", finish.Nonfoil); n == 0 {
		t.Error("the imported cards have no price point")
	}
}

func TestDryRunsRecordNothing(t *testing.T) {
	d := deckDeps(t, solRing())

	if err := d.Store.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	deck := parseDeck(t, "Ramp", "1 Sol Ring\n")

	if _, err := DeckAdd(context.Background(), d, nil, deck, DeckAddOptions{DryRun: true}); err != nil {
		t.Fatalf("DeckAdd: %v", err)
	}
	if n := addedSeries(t, d, "sol-id-1", finish.Nonfoil); n != 0 {
		t.Errorf("a dry run wrote %d price points", n)
	}
}
