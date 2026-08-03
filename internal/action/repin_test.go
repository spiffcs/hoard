package action

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// fakePrints answers SearchPrints from a fixture map of name → printings.
type fakePrints struct {
	prints map[string][]scryfall.Card
}

func (f fakePrints) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	return f.prints[name], nil
}

// RepinDeck re-points off-set printings at the named set, counts the ones
// already there, reports the names the set never printed, and picks the
// lowest collector number when the set printed a name more than once.
func TestRepinDeck(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	elves := scryfall.Card{ID: "we-hob", Set: "hob", CollectorNumber: "142",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	solRight := scryfall.Card{ID: "sol-cma", Set: "cma", CollectorNumber: "263",
		Name: "Sol Ring", ScryfallURL: "http://x"}
	oddball := scryfall.Card{ID: "odd-mh3", Set: "mh3", CollectorNumber: "1",
		Name: "Oddball", ScryfallURL: "http://x"}
	forest := scryfall.Card{ID: "forest-fdn", Set: "fdn", CollectorNumber: "280",
		Name: "Forest", ScryfallURL: "http://x"}
	if err := st.UpsertPrintings([]scryfall.Card{elves, solRight, oddball, forest}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	deckID, err := st.UpsertDeck(store.DeckMeta{Name: "Guided by Nature", Source: "text", SourceID: "gbn"},
		[]store.Entry{
			{ScryfallID: "we-hob", Finish: "nonfoil", Board: "main", Quantity: 1},
			{ScryfallID: "sol-cma", Finish: "nonfoil", Board: "main", Quantity: 1},
			{ScryfallID: "odd-mh3", Finish: "nonfoil", Board: "main", Quantity: 1},
			{ScryfallID: "forest-fdn", Finish: "nonfoil", Board: "main", Quantity: 5},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	prints := fakePrints{prints: map[string][]scryfall.Card{
		"Wood Elves": {
			elves,
			{ID: "we-cma", Set: "cma", CollectorNumber: "154", Name: "Wood Elves", ScryfallURL: "http://x"},
		},
		"Sol Ring": {solRight},
		"Oddball":  {oddball}, // no cma printing exists
		"Forest": { // two cma basics: the lowest collector number wins
			{ID: "forest-cma2", Set: "cma", CollectorNumber: "300", Name: "Forest", ScryfallURL: "http://x"},
			{ID: "forest-cma1", Set: "cma", CollectorNumber: "299", Name: "Forest", ScryfallURL: "http://x"},
			forest,
		},
	}}

	res, err := RepinDeck(context.Background(), st, prints, "Guided by Nature", "CMA")
	if err != nil {
		t.Fatalf("RepinDeck: %v", err)
	}
	if res.Total != 4 || res.Already != 1 || res.Repinned != 2 || res.Moved != 2 {
		t.Errorf("result = %+v, want 4 total, 1 already, 2 repinned/moved", res)
	}
	if !slices.Equal(res.Missing, []string{"Oddball"}) {
		t.Errorf("missing = %v, want Oddball alone", res.Missing)
	}

	entries, err := st.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Card.Name] = e.Card.ScryfallID
	}
	want := map[string]string{
		"Wood Elves": "we-cma", "Sol Ring": "sol-cma",
		"Oddball": "odd-mh3", "Forest": "forest-cma1",
	}
	for name, sid := range want {
		if got[name] != sid {
			t.Errorf("%s = %s, want %s", name, got[name], sid)
		}
	}

	// A second run is a no-op: everything resolvable is already on the set.
	res, err = RepinDeck(context.Background(), st, prints, "Guided by Nature", "cma")
	if err != nil {
		t.Fatalf("second RepinDeck: %v", err)
	}
	if res.Repinned != 0 || res.Already != 3 {
		t.Errorf("second run = %+v, want nothing to move", res)
	}
}
