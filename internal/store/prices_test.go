package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// Etched stopped borrowing the foil price in v21. A card whose etched product
// trades well away from its foil one was carried at the foil number in every
// total hoard reports, while the comps sheet on the same screen read the
// vendors' own etched bucket — the portfolio was the last place still folding
// the two together.
func TestEtchedIsValuedFromItsOwnPrice(t *testing.T) {
	s := newTestStore(t)
	kenrith := scryfall.Card{
		ID: "kenrith-id", Set: "cmr", CollectorNumber: "332", Name: "Kenrith",
		PriceUSD:       f(1.50),
		PriceUSDFoil:   f(4.00),
		PriceUSDEtched: f(30.00),
		ScryfallURL:    "https://scryfall.com/card/cmr/332",
	}
	if err := s.AddCardFinish(kenrith, "etched", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Value != 60 {
		t.Errorf("value = %v, want 60 (2 × the etched price), not 8 (the foil one)", totals.Value)
	}

	// Nothing is unpriced: the etched column answers for the etched copy.
	un, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unpriced = %+v, want none", un)
	}
}

// Not every source splits the product, so an etched holding of a printing the
// feed prices only as a foil must keep reading the foil column — the fallback
// is what makes v21 a strict improvement rather than a new way to read $0.00.
func TestEtchedFallsBackToFoilWhenUnsplit(t *testing.T) {
	s := newTestStore(t)
	c := ulamog() // foil 25.00, no etched figure
	if err := s.AddCardFinish(c, "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Value != 25 {
		t.Errorf("value = %v, want the foil price 25 as the fallback", totals.Value)
	}
	un, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unpriced = %+v, want none: the foil fallback prices it", un)
	}
}

// PriceSources classifies each holding exactly as entryValue prices it, so the
// coverage breakdown and the total it explains cannot disagree.
func TestPriceSourcesCountsEtchedAsPriced(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	c.PriceUSDEtched = f(30.00)
	if err := s.AddCardFinish(c, "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	srcs, err := s.PriceSources()
	if err != nil {
		t.Fatalf("PriceSources: %v", err)
	}
	if len(srcs) != 1 || srcs[0].Source != "scryfall" || srcs[0].Printings != 1 {
		t.Errorf("sources = %+v, want one scryfall-priced printing", srcs)
	}
}
