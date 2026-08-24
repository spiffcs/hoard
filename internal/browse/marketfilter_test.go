package browse

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

func marketCard(name, set string, fin finish.Finish, copies int, value float64) store.OwnedFinish {
	return store.OwnedFinish{
		ScryfallID: strings.ToLower(name) + "-id", Name: name,
		SetCode: set, CollectorNumber: "1", Finish: fin,
		Copies: copies, Value: value,
	}
}

func marketOpp(c store.OwnedFinish, mkt, sell float64) market.Opportunity {
	return market.Opportunity{
		Card: c, Market: mkt, BuyAt: mkt, BuyFrom: "tcgplayer",
		SellAt: sell, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true,
	}
}

func marketComp(c store.OwnedFinish, low, buylist float64) market.Comp {
	return market.Comp{
		Card: c, Market: low, HasMarket: true, Low: low, LowFrom: "tcgplayer",
		Manapool: low + 1, HasManapool: true,
		Buylist: buylist, BuylistTo: "cardkingdom", HasBuylist: true,
	}
}

func marketFilterResult() market.Result {
	riser := marketCard("Riser", "aaa", finish.Nonfoil, 2, 20)
	sinker := marketCard("Sinker", "bbb", finish.Foil, 1, 50)
	brainstorm := marketCard("Brainstorm", "aaa", finish.Nonfoil, 4, 40)
	return market.Result{
		Opportunities: []market.Opportunity{
			marketOpp(riser, 10, 15),
			marketOpp(sinker, 10, 8),
		},
		Comps: []market.Comp{
			marketComp(riser, 10, 8),
			marketComp(sinker, 10, 8),
			marketComp(brainstorm, 5, 4),
		},
		Compared: 3,
	}
}

func onMarket(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m := atAllCards(t, newTestModel(t, st))
	m.view = viewMarket
	m.marketResult = marketFilterResult()
	m.marketLoaded = true
	m.focus = paneCards
	m.applyMarketRows()
	if len(m.marketAllRows) != 2 || len(m.marketAllComps) != 3 {
		t.Fatalf("seeded market = %d rows, %d comps; want 2 and 3",
			len(m.marketAllRows), len(m.marketAllComps))
	}
	return m
}

func marketNames(rows []market.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Card.Name)
	}
	return out
}

func compNames(rows []market.Comp) []string {
	out := make([]string, 0, len(rows))
	for _, c := range rows {
		out = append(out, c.Card.Name)
	}
	return out
}

func TestMarketFilterNarrowsAllThreeTables(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "ris")

	if m.mode() != modeFilter {
		t.Fatalf("mode = %v, want the filter bar open", m.mode())
	}
	if m.filterErr != "" {
		t.Fatalf("filter error: %s", m.filterErr)
	}
	if got := marketNames(m.marketRows); len(got) != 1 || got[0] != "Riser" {
		t.Errorf("market rows = %v, want Riser alone", got)
	}
	if got := compNames(m.marketComps); len(got) != 1 || got[0] != "Riser" {
		t.Errorf("comps = %v, want Riser's sheet alone", got)
	}

	out := m.View()
	if !strings.Contains(out, "Riser") {
		t.Errorf("the matching row must still render:\n%s", out)
	}
	for _, gone := range []string{"Sinker", "Brainstorm"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q must not render under the filter %q:\n%s", gone, "ris", out)
		}
	}
}

func TestMarketFilterReachesTheCompsAlone(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "brain")

	if len(m.marketRows) != 0 {
		t.Errorf("market rows = %v, want none — Brainstorm is on no Kind table",
			marketNames(m.marketRows))
	}
	if got := compNames(m.marketComps); len(got) != 1 || got[0] != "Brainstorm" {
		t.Fatalf("comps = %v, want Brainstorm's sheet alone", got)
	}
	if out := m.View(); !strings.Contains(out, "Brainstorm") {
		t.Errorf("the comps row must render:\n%s", out)
	}
}

func TestMarketFilterHonoursFieldTerms(t *testing.T) {
	tests := []struct {
		query     string
		wantRows  []string
		wantComps []string
	}{
		{"set:aaa", []string{"Riser"}, []string{"Riser", "Brainstorm"}},
		{"finish:foil", []string{"Sinker"}, []string{"Sinker"}},
		{"qty>=2", []string{"Riser"}, []string{"Riser", "Brainstorm"}},
		{"value>45", []string{"Sinker"}, []string{"Sinker"}},
		{"name:sink", []string{"Sinker"}, []string{"Sinker"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			m := onMarket(t, testStore())
			m = typeFilter(m, tt.query)
			if m.filterErr != "" {
				t.Fatalf("filter error: %s", m.filterErr)
			}
			assertSameNames(t, "market rows", marketNames(m.marketRows), tt.wantRows)
			assertSameNames(t, "comps", compNames(m.marketComps), tt.wantComps)
		})
	}
}

func TestMarketFilterHonoursTraitTerms(t *testing.T) {
	st := testStore()
	st.traits = map[string][]string{
		"riser-id":      {"mythic", "creature"},
		"sinker-id":     {"rare", "instant"},
		"brainstorm-id": {"common", "instant"},
	}
	m := onMarket(t, st)
	m = typeFilter(m, "rarity:mythic")
	if m.filterErr != "" {
		t.Fatalf("filter error: %s", m.filterErr)
	}
	assertSameNames(t, "market rows", marketNames(m.marketRows), []string{"Riser"})
	assertSameNames(t, "comps", compNames(m.marketComps), []string{"Riser"})
}

func TestMarketFilterRefusesPriceByName(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "price<5")
	if m.filterErr != "" {
		t.Fatalf("price<5 must parse — it is a real key: %s", m.filterErr)
	}
	out := m.View()
	if !strings.Contains(out, "does not apply on market") {
		t.Errorf("the bar must name the term market cannot answer:\n%s", out)
	}
	if !strings.Contains(out, "four prices") {
		t.Errorf("the refusal must say why, not just decline:\n%s", out)
	}
}

func TestMarketPriceRefusalSurvivesTheBarClosing(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "price<5")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode() == modeFilter {
		t.Fatal("enter must close the bar")
	}

	out := m.View()
	if strings.Contains(out, "none match price<5") {
		t.Errorf("a refused term must not read as a query that matched nothing:\n%s", out)
	}
	if !strings.Contains(out, "does not apply on market") {
		t.Errorf("the refusal must still be on screen with the bar closed:\n%s", out)
	}
	if !strings.Contains(out, "four prices") {
		t.Errorf("the reason must survive too, not just the refusal:\n%s", out)
	}
}

func TestMarketFilterRefusesBoard(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "board:main")
	if m.filterErr != "" {
		t.Fatalf("board:main must parse — it is a real key: %s", m.filterErr)
	}
	if out := m.View(); !strings.Contains(out, "does not apply on market") {
		t.Errorf("the bar must name the term market cannot answer:\n%s", out)
	}
}

func TestMarketEmptiedByFilterReadsAsFiltered(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "zzz")
	if len(m.marketRows) != 0 || len(m.marketComps) != 0 {
		t.Fatalf("market = %v / %v, want nothing to match",
			marketNames(m.marketRows), compNames(m.marketComps))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	out := m.View()
	if strings.Contains(out, "nothing today") {
		t.Errorf("an emptied-by-filter market must not blame the vendors:\n%s", out)
	}
	if !strings.Contains(out, "none match zzz") {
		t.Errorf("an emptied-by-filter market must say it is filtered:\n%s", out)
	}

	if !strings.Contains(out, market.CompsTitle) {
		t.Errorf("the emptied tables must keep their headings:\n%s", out)
	}
}

func TestMarketFilterEscRestores(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "ris")
	if len(m.marketRows) != 1 {
		t.Fatalf("market rows = %v, want the filter to have bitten first",
			marketNames(m.marketRows))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode() == modeFilter {
		t.Error("esc must close the bar")
	}
	if len(m.marketRows) != 2 || len(m.marketComps) != 3 {
		t.Errorf("market = %v / %v, want everything back",
			marketNames(m.marketRows), compNames(m.marketComps))
	}
}

func TestMarketFilterSurvivesEnter(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "ris")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode() == modeFilter {
		t.Fatal("enter must close the bar")
	}
	if got := marketNames(m.marketRows); len(got) != 1 || got[0] != "Riser" {
		t.Errorf("market rows = %v, want the query still applied", got)
	}
}

func TestMarketStatusNamesTheLiveFilter(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "ris")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.mode() == modeFilter {
		t.Fatal("enter must close the bar")
	}
	if out := m.View(); !strings.Contains(out, "filtered by ris") {
		t.Errorf("the status line must name the live query:\n%s", out)
	}
}

func TestMarketFilterMatchCount(t *testing.T) {
	m := onMarket(t, testStore())
	if got, want := m.filterMatchCount(), 5; got != want {
		t.Errorf("unfiltered market count = %d, want %d (2 rows + 3 comps)", got, want)
	}

	m = typeFilter(m, "ris")
	if got, want := m.filterMatchCount(), 2; got != want {
		t.Errorf("filtered market count = %d, want %d", got, want)
	}
	if out := m.View(); !strings.Contains(out, "2 match") {
		t.Errorf("the filter bar must count the market rows it selected:\n%s", out)
	}
}

func TestMarketFilterMatchCountIsTheWholeResult(t *testing.T) {
	m := onMarket(t, testStore())

	var comps []market.Comp
	for i := range pageSize + 10 {
		comps = append(comps, marketComp(
			marketCard("Fixture", "aaa", finish.Nonfoil, 1, float64(100-i)), 10, 8))
	}
	res := m.marketResult
	res.Comps = comps
	m.marketResult = res
	m.applyMarketRows()

	m = typeFilter(m, "fixture")
	if got, want := len(m.marketComps), pageSize; got != want {
		t.Fatalf("visible comps = %d, want one page of %d", got, want)
	}
	if got, want := m.filterMatchCount(), pageSize+10; got != want {
		t.Errorf("match count = %d, want the whole filtered result %d", got, want)
	}
}

func assertSameNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
}
