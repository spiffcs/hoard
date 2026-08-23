package pricing

import (
	"testing"

	"github.com/spiffcs/hoard/internal/tcgcsv"
)

func TestAskPrefersDirectInventory(t *testing.T) {
	q := tcgcsv.Quote{Market: 610.49, Low: 400, Mid: 1015.29, Direct: 900}

	got, ok := Ask(q)
	if !ok || got != 900 {
		t.Errorf("Ask = %v (ok=%v), want the condition-verified Direct price", got, ok)
	}
}

func TestAskFallsBackToAHealthyLowListing(t *testing.T) {
	// Grief, SLD 7006 foil: no Direct listing, low/mid = 0.87.
	q := tcgcsv.Quote{Market: 610.49, Low: 882.86, Mid: 1015.29}

	got, ok := Ask(q)
	if !ok || got != 882.86 {
		t.Errorf("Ask = %v (ok=%v), want the cheapest listing when the spread is tight", got, ok)
	}
}

func TestAskRefusesAListingFarBelowTheMidpoint(t *testing.T) {
	q := tcgcsv.Quote{Market: 100, Low: 20, Mid: 100}

	if got, ok := Ask(q); ok {
		t.Errorf("Ask = %v (ok=%v), want a refusal: a listing at 20%% of mid is not a NM copy",
			got, ok)
	}
}

func TestAskRefusesWhenThereIsNothingToJudge(t *testing.T) {
	for name, q := range map[string]tcgcsv.Quote{
		"no low at all":  {Market: 100, Mid: 100},
		"no mid to test": {Market: 100, Low: 20},
		"empty":          {},
	} {
		if got, ok := Ask(q); ok {
			t.Errorf("%s: Ask = %v (ok=%v), want a refusal", name, got, ok)
		}
	}
}

func TestAskTakesDirectEvenWhenTheLowWouldBeRefused(t *testing.T) {
	q := tcgcsv.Quote{Market: 100, Low: 20, Mid: 100, Direct: 95}

	got, ok := Ask(q)
	if !ok || got != 95 {
		t.Errorf("Ask = %v (ok=%v), want Direct to stand on its own", got, ok)
	}
}
