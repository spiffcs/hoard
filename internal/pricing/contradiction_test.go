package pricing

import (
	"testing"

	"github.com/spiffcs/hoard/internal/tcgcsv"
)

// The controls, and the negative half matters more than the positive one.
//
// Every figure below was measured against tcgcsv on 2026-08-12 over the 2,604
// owned TCGplayer product/subtype pairs in a live hoard. Thirty-nine of them
// quote a market price under the cheapest ask; thirty-eight are healthy. A
// detector that fires on the healthy rows would not be a safety feature, it
// would silently inflate thirty-eight holdings — so those are the cases with
// teeth, and they are pinned here by name and number rather than invented.
func TestContradictedRefusesOnlyTheDefect(t *testing.T) {
	cases := []struct {
		name         string
		market, low  float64
		wantContrary bool
	}{
		// The defect. Released two days after it was read, never sold, so the
		// market price is an average over nothing while $97.55 buys one today.
		{"Aragorn and Arwen Wedded, hoc #68 surge foil", 0.56, 97.55, true},

		// The healthy rows. Thin markets where the last completed sale sits
		// under today's listings — the ordinary state of an illiquid old foil,
		// and none of these may be touched.
		{"The Lonely Mountain, hob #207 normal (worst healthy, 3.3x)", 3.89, 12.98, false},
		{"Chief of the Wilds, hoc #103 normal", 37.34, 83.49, false},
		{"Mind's Desire, scg #41 foil", 79.49, 169.99, false},
		{"Cryptolith Rite, soi #200 foil", 6.38, 13.00, false},
		{"Caller of the Claw, lgn #121 foil", 40.34, 80.00, false},
		{"Devastating Dreams, tor #95 foil", 23.95, 31.17, false},
		{"Bitterblossom, uma #85 foil", 45.56, 54.49, false},
		{"Island, cma #293 normal (cheap and thin)", 0.15, 0.20, false},

		// The ordinary case: an ask above the last sale, which is what a
		// liquid market looks like and must never be read as a contradiction.
		{"market above the cheapest ask", 17.56, 15.00, false},

		// Boundaries. The ratio is inclusive, so exactly 20x refuses.
		{"exactly the ratio", 1.00, 20.00, true},
		{"just under the ratio", 1.00, 19.99, false},

		// Nothing to compare against. A feed row without an ask, or without a
		// market price, cannot contradict anything.
		{"no ask published", 0.56, 0, false},
		{"no market price", 0, 97.55, false},
		{"neither", 0, 0, false},
	}
	for _, tc := range cases {
		if got := Contradicted(tc.market, tc.low); got != tc.wantContrary {
			t.Errorf("Contradicted(%v, %v) = %v, want %v — %s",
				tc.market, tc.low, got, tc.wantContrary, tc.name)
		}
	}
}

// Resolve substitutes the cheapest ask for a refused market price and reports
// what it refused, so the refusal can be recorded rather than applied silently.
func TestResolveSubstitutesTheCheapestAsk(t *testing.T) {
	q := tcgcsv.Quote{Market: 0.56, Low: 97.55, Mid: 100.07, High: 435.00}
	price, refused := Resolve(q)
	if price != 97.55 {
		t.Errorf("price = %v, want the cheapest ask 97.55", price)
	}
	if refused != 0.56 {
		t.Errorf("refused = %v, want the market price it replaced", refused)
	}

	// A healthy quote passes through untouched and reports no refusal, so a
	// caller can use refused != 0 as the test for "something was replaced".
	q = tcgcsv.Quote{Market: 3.89, Low: 12.98, Mid: 17.36}
	if price, refused = Resolve(q); price != 3.89 || refused != 0 {
		t.Errorf("Resolve(healthy) = %v, %v; want 3.89, 0", price, refused)
	}

	// Detected but uncorrectable: hoard has no ask to fall back on, so it
	// keeps the market price rather than zeroing a card it knows is held.
	q = tcgcsv.Quote{Market: 0.56}
	if price, refused = Resolve(q); price != 0.56 || refused != 0 {
		t.Errorf("Resolve(no ask) = %v, %v; want 0.56, 0", price, refused)
	}
}
