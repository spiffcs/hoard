package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func unpriceable() scryfall.Card {
	return scryfall.Card{
		ID: "kenrith-id", Set: "cmr", CollectorNumber: "332", Name: "Kenrith",
		ScryfallURL: "https://scryfall.com/card/cmr/332",
	}
}

func TestAValidFinishWithNoPriceIsReportedUnpriced(t *testing.T) {
	all := finish.All()
	if len(all) < 3 {
		t.Fatalf("finish.All() = %v, want at least nonfoil, foil and etched", all)
	}
	for _, fin := range all {
		t.Run(fin.String(), func(t *testing.T) {
			s := newTestStore(t)
			if err := s.AddCardFinish(unpriceable(), fin, 1); err != nil {
				t.Fatalf("AddCardFinish(%q): %v", fin, err)
			}

			un, err := s.Unpriced()
			if err != nil {
				t.Fatalf("Unpriced: %v", err)
			}
			if len(un) != 1 || un[0].ScryfallID != "kenrith-id" {
				t.Errorf("unpriced = %+v, want the %q holding: no source can price it", un, fin)
			}

			totals, err := s.CollectionTotals()
			if err != nil {
				t.Fatalf("CollectionTotals: %v", err)
			}
			if totals.Value != 0 {
				t.Errorf("value = %v, want 0: a %q copy nothing can price contributes nothing",
					totals.Value, fin)
			}

			srcs, err := s.PriceSources()
			if err != nil {
				t.Fatalf("PriceSources: %v", err)
			}
			var unpricedBucket SourceCount
			for _, src := range srcs {
				if src.Source == "" {
					unpricedBucket = src
					continue
				}
				t.Errorf("sources = %+v, want %q credited to no source: nothing can price it",
					srcs, fin)
			}
			if unpricedBucket.Printings != 1 || unpricedBucket.Copies != 1 {
				t.Errorf("unpriced bucket = %+v, want the one %q copy: the empty source is "+
					"what the valuation report renders as its unpriced row", unpricedBucket, fin)
			}
		})
	}
}
