package command

import (
	"math"
	"testing"
)

func TestCostBasisIsReadableStraightAfterImport(t *testing.T) {
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)

	if err := importCmd(st, "--preserve-binders", manaboxRoundTripFixture); err != nil {
		t.Fatalf("hoard import: %v", err)
	}

	depth, _, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}

	rows, err := st.CostBasisMovers()
	if err != nil {
		t.Fatalf("CostBasisMovers: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("the cost basis window is empty straight after an import that "+
			"carried prices; history depth was %d", depth)
	}

	want := map[string]float64{
		"Sol Ring nonfoil":       (4.25*2 + 9.99) / 3,
		"Lightning Bolt nonfoil": (0.75 + 0.40) / 2,
		"Lightning Bolt foil":    1.50,
		"Mystic Remora nonfoil":  (12.00 + 3.00) / 2,
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Name+" "+r.Finish.String()] = r.Old
	}
	for key, w := range want {
		g, ok := got[key]
		if !ok {
			t.Errorf("%s has a cost basis but no row; got %v", key, got)
			continue
		}
		if math.Abs(g-w) > 0.005 {
			t.Errorf("%s cost basis = %.4f, want the weighted average %.4f", key, g, w)
		}
	}
}
