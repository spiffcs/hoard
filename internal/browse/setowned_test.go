package browse

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func eoeStore() *fakeStore {
	return &fakeStore{
		collection: []store.CollectionRow{
			row("Alpha", "eoe", "1", finish.Nonfoil, 1, 10),
			row("Alpha", "eoe", "1", finish.Foil, 1, 25),
			row("Beta", "eoe", "2", finish.Nonfoil, 2, 20),
		},
		unowned: map[string][]store.UnownedRow{
			"eoe": {{CollectionRow: row("Gamma", "eoe", "3", finish.Nonfoil, 3, 0), Where: "Want"}},
		},
	}
}

func eoeModel(t *testing.T, f *fakeStore, opts ...Option) Model {
	t.Helper()
	m, err := New(f, append([]Option{WithEnv(ui.Env{Color: true})}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = atSet(t, next.(Model), "eoe")
	if title, _ := m.viewHeader(); title != "CARDS · EOE" {
		t.Fatalf("header title = %q, want the eoe set selected", title)
	}
	return m
}

func TestSetCardsHeaderCountsOwnedPrintings(t *testing.T) {
	m := eoeModel(t, eoeStore())

	if got := cardNames(m.filteredCards); len(got) != 3 {
		t.Errorf("cards = %v, want all three rows — Alpha in two finishes, plus Beta", got)
	}
	_, totals := m.viewHeader()
	if !strings.Contains(totals, "2/3 owned") {
		t.Errorf("totals = %q, want it to say 2/3 owned", totals)
	}
}

func TestBFlipsSetCardsToTheOnesYouDoNotOwn(t *testing.T) {
	m := key(eoeModel(t, eoeStore()), "b")

	got := cardNames(m.filteredCards)
	if len(got) != 1 || got[0] != "Gamma" {
		t.Fatalf("cards = %v, want just Gamma — the printing waiting in a want binder", got)
	}
	if title, _ := m.viewHeader(); title != "CARDS · EOE" {
		t.Errorf("header title = %q, want it unchanged by the flip", title)
	}
	_, totals := m.viewHeader()
	if !strings.Contains(totals, "1/3 unowned") {
		t.Errorf("totals = %q, want it to say 1/3 unowned", totals)
	}

	back := key(m, "b")
	if got := cardNames(back.filteredCards); len(got) != 3 {
		t.Errorf("after flipping back, cards = %v, want the three owned rows", got)
	}
	if _, totals := back.viewHeader(); !strings.Contains(totals, "2/3 owned") {
		t.Errorf("after flipping back, totals = %q, want 2/3 owned", totals)
	}
}

func TestSetUnownedWidensToTheWholeSetWhenTheCatalogKnowsIt(t *testing.T) {
	prints := func(_ context.Context, code string) ([]scryfall.Card, error) {
		if code != "eoe" {
			return nil, nil
		}
		out := make([]scryfall.Card, 0, 4)
		for _, p := range [][2]string{{"Alpha", "1"}, {"Beta", "2"}, {"Gamma", "3"}, {"Delta", "4"}} {
			out = append(out, scryfall.Card{
				ID: p[0] + "-id", Name: p[0], Set: "eoe", CollectorNumber: p[1],
				ScryfallURL: "https://scryfall.com/card/eoe/" + p[1],
			})
		}
		return out, nil
	}

	m := eoeModel(t, eoeStore(), WithSetPrints(prints))
	if _, totals := m.viewHeader(); !strings.Contains(totals, "2/4 owned") {
		t.Errorf("totals = %q, want 2/4 owned — the catalog knows a fourth printing", totals)
	}

	m = key(m, "b")
	got := cardNames(m.filteredCards)
	if len(got) != 2 || !strings.Contains(strings.Join(got, " "), "Delta") {
		t.Fatalf("cards = %v, want Gamma (wanted) and Delta (never held)", got)
	}
	if _, totals := m.viewHeader(); !strings.Contains(totals, "2/4 unowned") {
		t.Errorf("totals = %q, want 2/4 unowned", totals)
	}
}
