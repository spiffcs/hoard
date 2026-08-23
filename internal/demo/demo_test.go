package demo

import (
	"bytes"
	"testing"

	"github.com/spiffcs/hoard/internal/hoardjson"
)

func TestCollectionIsAUsableHoardDocument(t *testing.T) {
	h, err := hoardjson.ReadHoard(bytes.NewReader(Collection))
	if err != nil {
		t.Fatalf("embedded collection is not a hoard document: %v", err)
	}

	if len(h.Printings) == 0 {
		t.Fatal("no printings: the demo would open on an empty collection")
	}
	if len(h.Holdings.Rows) == 0 {
		t.Fatal("no holdings: printings without holdings are a compendium, not a collection")
	}

	var binders, decks int
	for _, c := range h.Containers {
		switch c.Kind {
		case "binder":
			binders++
		case "deck":
			decks++
		}
	}
	if binders == 0 || decks == 0 {
		t.Errorf("containers = %d binders, %d decks; want at least one of each", binders, decks)
	}

	for _, p := range h.Printings {
		if p.Name == "" {
			t.Errorf("printing %s has no name", p.ScryfallID)
		}
		if len(p.Raw) == 0 {
			t.Errorf("printing %q carries no raw card document; the browser would render it blank", p.Name)
		}
	}

	var priced int
	for _, p := range h.Printings {
		if p.PriceUsd != nil || p.PriceUsdFoil != nil || p.PriceUsdEtched != nil {
			priced++
		}
	}
	if priced == 0 {
		t.Error("no printing carries a price: the demo would value the whole collection at nothing")
	}
}

func TestHistoryDescribesTheCollection(t *testing.T) {
	doc, err := ReadHistory(bytes.NewReader(History))
	if err != nil {
		t.Fatalf("embedded history: %v", err)
	}
	if len(doc.Retail) == 0 {
		t.Fatal("no retail series: movers would open empty")
	}

	h, err := hoardjson.ReadHoard(bytes.NewReader(Collection))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, p := range h.Printings {
		known[p.ScryfallID] = true
	}
	for _, s := range append(append([]HistorySeries(nil), doc.Retail...), doc.Bids...) {
		if !known[s.ScryfallID] {
			t.Errorf("history carries a series for %s (%s), which the collection does not hold — "+
				"regenerate it: task generate-demo-history", s.ScryfallID, s.Finish)
		}
		if len(s.Points) == 0 {
			t.Errorf("%s (%s) has an empty series", s.ScryfallID, s.Finish)
		}
		if s.Source == "" {
			t.Errorf("%s (%s) names no vendor", s.ScryfallID, s.Finish)
		}
	}

	covered := map[string]bool{}
	for _, s := range doc.Retail {
		covered[s.ScryfallID] = true
	}
	for id := range known {
		if !covered[id] {
			t.Errorf("no price history for printing %s; movers cannot chart it", id)
		}
	}
}
