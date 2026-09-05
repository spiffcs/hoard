package tui

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func fourFigure() scryfall.Card {
	v, foil := 1234.00, 2500.50
	return scryfall.Card{
		ID: "lotus", Set: "lea", CollectorNumber: "232", Name: "Black Lotus",
		PriceUSD: &v, PriceUSDFoil: &foil,
	}
}

func TestTheAddCascadeGroupsThousandsLikeTheBrowser(t *testing.T) {
	if got := priceForFinish(fourFigure(), finish.Nonfoil); got != "$1,234.00" {
		t.Errorf("priceForFinish = %q, want $1,234.00", got)
	}
	if got := priceLabel(fourFigure()); !strings.Contains(got, "$1,234.00") {
		t.Errorf("priceLabel = %q, want it to contain $1,234.00", got)
	}
	if got := priceLabel(fourFigure()); !strings.Contains(got, "$2,500.50") {
		t.Errorf("priceLabel = %q, want it to contain $2,500.50", got)
	}
}

func TestTheSessionTallyGroupsThousands(t *testing.T) {
	m := model{addedCount: 3, addedValue: 1234}
	if got := m.sessionTally(); !strings.Contains(got, "$1,234.00") {
		t.Errorf("sessionTally = %q, want it to contain $1,234.00", got)
	}
}

func TestTheAutoAddedCounterGroupsThousands(t *testing.T) {
	m := model{addedValue: 1234, tally: []string{"a card"}}
	if got := m.autoAddedCounter(); !strings.Contains(got, "$1,234.00") {
		t.Errorf("counter = %q, want it to contain $1,234.00", got)
	}
}

func TestAnAbsentPriceStillReadsAsUnknown(t *testing.T) {
	c := scryfall.Card{ID: "x", Name: "No Price"}
	if got := priceForFinish(c, finish.Nonfoil); got != "—" {
		t.Errorf("priceForFinish with no price = %q, want an em dash", got)
	}
}
