package browse

import (
	"context"
	"slices"
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

func TestSetCardsHeaderCountsOwnedFinishes(t *testing.T) {
	m := eoeModel(t, eoeStore())

	if got := cardNames(m.filteredCards); len(got) != 3 {
		t.Errorf("cards = %v, want all three rows — Alpha in two finishes, plus Beta", got)
	}
	_, totals := m.viewHeader()
	if !strings.Contains(totals, "3/4 owned") {
		t.Errorf("totals = %q, want it to say 3/4 owned — Alpha counts twice because "+
			"its nonfoil and its foil are separate things to own, and Gamma is the "+
			"fourth", totals)
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
	if !strings.Contains(totals, "1/4 unowned") {
		t.Errorf("totals = %q, want it to say 1/4 unowned", totals)
	}

	back := key(m, "b")
	if got := cardNames(back.filteredCards); len(got) != 3 {
		t.Errorf("after flipping back, cards = %v, want the three owned rows", got)
	}
	if _, totals := back.viewHeader(); !strings.Contains(totals, "3/4 owned") {
		t.Errorf("after flipping back, totals = %q, want 3/4 owned", totals)
	}
}

func TestSetUnownedWidensToTheWholeSetWhenTheCatalogKnowsIt(t *testing.T) {
	prints := func(_ context.Context, code string) ([]scryfall.Card, error) {
		if code != "eoe" {
			return nil, nil
		}
		out := make([]scryfall.Card, 0, 4)
		for _, p := range [][3]string{
			{"Alpha", "1", "both"}, {"Beta", "2", ""},
			{"Gamma", "3", ""}, {"Delta", "4", ""},
		} {
			fins := []string{"nonfoil"}
			if p[2] == "both" {
				fins = []string{"nonfoil", "foil"}
			}
			out = append(out, scryfall.Card{
				ID: p[0] + "-id", Name: p[0], Set: "eoe", CollectorNumber: p[1],
				Finishes:    fins,
				ScryfallURL: "https://scryfall.com/card/eoe/" + p[1],
			})
		}
		return out, nil
	}

	m := eoeModel(t, eoeStore(), WithSetPrints(prints))
	if _, totals := m.viewHeader(); !strings.Contains(totals, "3/5 owned") {
		t.Errorf("totals = %q, want 3/5 owned — the catalog knows a fourth printing, "+
			"and Alpha sells in two finishes you both hold", totals)
	}

	m = key(m, "b")
	got := cardNames(m.filteredCards)
	if len(got) != 2 || !strings.Contains(strings.Join(got, " "), "Delta") {
		t.Fatalf("cards = %v, want Gamma (wanted) and Delta (never held)", got)
	}
	if _, totals := m.viewHeader(); !strings.Contains(totals, "2/5 unowned") {
		t.Errorf("totals = %q, want 2/5 unowned", totals)
	}
}

func cardNamed(t *testing.T, m Model, name string) card {
	t.Helper()
	for _, c := range m.filteredCards {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%s is not in the view: %v", name, cardNames(m.filteredCards))
	return card{}
}

func TestUnownedRowsCarryTheSameIdentityAsOwnedRows(t *testing.T) {
	f := eoeStore()
	gamma := f.unowned["eoe"][0]
	gamma.ColorIdentity = []string{"B"}
	gamma.Treatment = "surge"
	gamma.Finish = finish.Foil
	f.unowned["eoe"] = []store.UnownedRow{gamma}

	prints := func(_ context.Context, _ string) ([]scryfall.Card, error) {
		return []scryfall.Card{{
			ID: "Delta-id", Name: "Delta", Set: "eoe", CollectorNumber: "4",
			ScryfallURL:   "https://scryfall.com/card/eoe/4",
			ColorIdentity: []string{"U", "R"},
			PromoTypes:    []string{"galaxyfoil"},
			Finishes:      []string{"foil"},
			PriceUSDFoil:  price(12.50),
		}}, nil
	}

	m := key(eoeModel(t, f, WithSetPrints(prints)), "b")

	held := cardNamed(t, m, "Gamma")
	if !slices.Equal(held.ColorIdentity, []string{"B"}) {
		t.Errorf("Gamma's colour identity = %v, want [B] carried from the store row",
			held.ColorIdentity)
	}
	if held.Treatment != "surge" || held.Finish != finish.Foil {
		t.Errorf("Gamma = %s/%q, want a foil surge row", held.Finish, held.Treatment)
	}

	missing := cardNamed(t, m, "Delta")
	if !slices.Equal(missing.ColorIdentity, []string{"U", "R"}) {
		t.Errorf("Delta's colour identity = %v, want [U R] so its name and pips are coloured",
			missing.ColorIdentity)
	}
	if missing.Treatment != "galaxy" {
		t.Errorf("Delta's treatment = %q, want %q from its promo types",
			missing.Treatment, "galaxy")
	}
	if missing.Finish != finish.Foil {
		t.Errorf("Delta's finish = %s, want foil — the printing has no nonfoil", missing.Finish)
	}
	if missing.Price == nil || *missing.Price != 12.50 {
		t.Errorf("Delta's price = %v, want the foil price it is actually sold at", missing.Price)
	}
}

func TestUnownedViewShowsColourPipsNotBlanks(t *testing.T) {
	prints := func(_ context.Context, _ string) ([]scryfall.Card, error) {
		return []scryfall.Card{{
			ID: "Delta-id", Name: "Delta", Set: "eoe", CollectorNumber: "4",
			ScryfallURL:   "https://scryfall.com/card/eoe/4",
			ColorIdentity: []string{"U", "R"},
			PriceUSD:      price(3),
			Finishes:      []string{"nonfoil"},
		}}, nil
	}
	m := key(eoeModel(t, eoeStore(), WithSetPrints(prints)), "b")

	if got := ui.Pips(cardNamed(t, m, "Delta").ColorIdentity); got != "UR" {
		t.Errorf("Delta renders pips %q, want %q like every owned row", got, "UR")
	}
}

func eoePrints(cards ...scryfall.Card) SetPrintsFunc {
	return func(_ context.Context, _ string) ([]scryfall.Card, error) { return cards, nil }
}

func catalogPrint(name, num string, usd float64) scryfall.Card {
	return scryfall.Card{
		ID: name + "-id", Name: name, Set: "eoe", CollectorNumber: num,
		ColorIdentity: []string{"U"}, PriceUSD: price(usd), Finishes: []string{"nonfoil"},
	}
}

func TestUnownedRowsYouNeverHeldShowNoQuantityOrValue(t *testing.T) {
	f := eoeStore()
	f.unowned["eoe"] = nil
	m := key(eoeModel(t, f, WithSetPrints(
		eoePrints(catalogPrint("Cosmic Nexus", "4", 3.25),
			catalogPrint("Voidwing Drake", "5", 41)))), "b")

	lines := m.cardLines(96)
	for _, gone := range []string{"QTY", "VALUE"} {
		if strings.Contains(lines[0], gone) {
			t.Errorf("%s column survives: %q — nothing in this table is held", gone, lines[0])
		}
	}
	out := strings.Join(lines, "\n")
	for _, pad := range []string{"×0", "$0.00"} {
		if strings.Contains(out, pad) {
			t.Errorf("a card you have never held is padded with %q:\n%s", pad, out)
		}
	}
	if !strings.Contains(out, "$41.00") {
		t.Errorf("the price you would pay is missing:\n%s", out)
	}
}

func TestUnownedWantRowsKeepTheirQuantityAndValue(t *testing.T) {
	m := key(eoeModel(t, eoeStore(), WithSetPrints(
		eoePrints(catalogPrint("Cosmic Nexus", "4", 3.25)))), "b")

	lines := m.cardLines(96)
	for _, kept := range []string{"QTY", "VALUE"} {
		if !strings.Contains(lines[0], kept) {
			t.Errorf("%s column is gone: %q — Gamma is on a want list at ×3", kept, lines[0])
		}
	}
	for _, line := range lines[1:] {
		if strings.Contains(line, "Cosmic Nexus") && strings.Contains(line, "×0") {
			t.Errorf("the never-held row still reads ×0: %q", line)
		}
	}
}

func TestUnownedHeaderShowsWhatFinishingTheSetWouldCost(t *testing.T) {
	f := eoeStore()
	f.unowned["eoe"] = []store.UnownedRow{
		{CollectionRow: row("Gamma", "eoe", "3", finish.Nonfoil, 3, 27), Where: "Want"},
	}
	m := eoeModel(t, f, WithSetPrints(eoePrints(catalogPrint("Cosmic Nexus", "4", 3.25))))

	if _, totals := m.viewHeader(); !strings.Contains(totals, "$55.00") {
		t.Errorf("owned totals = %q, want the $55.00 you hold", totals)
	}

	m = key(m, "b")
	_, totals := m.viewHeader()
	if !strings.Contains(totals, "$12.25 to finish") {
		t.Errorf("unowned totals = %q, want $12.25 to finish — one Gamma at $9 plus one Nexus at $3.25",
			totals)
	}
	if strings.Contains(totals, "$55.00") {
		t.Errorf("unowned totals = %q, still reporting what the set is worth to you", totals)
	}
}
