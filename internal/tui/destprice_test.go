package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestDestinationPickerShowsThePriceOfTheChosenFinish(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Foily", Set: "sld", CollectorNumber: "1",
		Finishes:     []string{"foil"},
		PriceUSD:     fp(0.25),
		PriceUSDFoil: fp(2.41)}
	m := newModel(context.Background(), fakeSearcher{}, (&recordingAdder{}).add, nil, "Foily", destFixtures())

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateDestPick {
		t.Fatalf("state = %v, want stateDestPick", got.state)
	}

	view := got.viewContent()
	if !strings.Contains(view, "$2.41") {
		t.Errorf("binder picker should price the copy being added:\n%s", view)
	}
	if strings.Contains(view, "$0.25") {
		t.Errorf("binder picker showed the nonfoil price for a foil copy:\n%s", view)
	}
}

func TestDestinationPickerPricesTheFinishTheUserPicked(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Bolt", Set: "m11", CollectorNumber: "146",
		Finishes:     []string{"nonfoil", "foil"},
		PriceUSD:     fp(0.25),
		PriceUSDFoil: fp(2.41)}
	m := newModel(context.Background(), fakeSearcher{}, (&recordingAdder{}).add, nil, "Bolt", destFixtures())

	mm, _ := m.Update(printsMsg{name: "Bolt", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateFinishPick {
		t.Fatalf("state = %v, want stateFinishPick", got.state)
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateDestPick {
		t.Fatalf("state = %v, want stateDestPick after choosing nonfoil", got.state)
	}

	view := got.viewContent()
	if !strings.Contains(view, "$0.25") {
		t.Errorf("binder picker should show the nonfoil price:\n%s", view)
	}
	if strings.Contains(view, "$2.41") {
		t.Errorf("binder picker showed the foil price for a nonfoil copy:\n%s", view)
	}
}
