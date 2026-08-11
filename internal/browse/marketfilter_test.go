package browse

// The typed query over the market view's three tables. The two Kind tables
// and the comp sheet all key by the same owned printing, so one query
// narrows all three — and the terms a vendor spread cannot answer are
// refused by name rather than answered with one of four prices.

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

// marketCard is one owned printing, distinguishable from its neighbours by
// name, set, finish, copies and value at once — so a single query can be
// shown to select a subset rather than happening to select everything.
func marketCard(name, set, finish string, copies int, value float64) store.OwnedFinish {
	return store.OwnedFinish{
		ScryfallID: strings.ToLower(name) + "-id", Name: name,
		SetCode: set, CollectorNumber: "1", Finish: finish,
		Copies: copies, Value: value,
	}
}

// marketOpp builds an opportunity over one printing. sell against market
// picks the table: paying over the last-sold price is PROFIT, paying under
// it but at least the liquid floor is BUYLIST.
func marketOpp(c store.OwnedFinish, mkt, sell float64) market.Opportunity {
	return market.Opportunity{
		Card: c, Market: mkt, BuyAt: mkt, BuyFrom: "tcgplayer",
		SellAt: sell, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true,
	}
}

// marketComp builds a comp sheet over the same printing, so the third
// table's rows key by card exactly as the other two do.
func marketComp(c store.OwnedFinish, low, buylist float64) market.Comp {
	return market.Comp{
		Card: c, Market: low, HasMarket: true, Low: low, LowFrom: "tcgplayer",
		Manapool: low + 1, HasManapool: true,
		Buylist: buylist, BuylistTo: "cardkingdom", HasBuylist: true,
	}
}

// marketFilterResult seeds one row in each of the three tables: Riser pays
// over the sales price (PROFIT), Sinker pays 80% of it (BUYLIST), and all
// three printings carry a comp sheet — including Brainstorm, which is on no
// Kind table at all, so a query can be seen to reach the comps on its own.
func marketFilterResult() market.Result {
	riser := marketCard("Riser", "aaa", "nonfoil", 2, 20)
	sinker := marketCard("Sinker", "bbb", "foil", 1, 50)
	brainstorm := marketCard("Brainstorm", "aaa", "nonfoil", 4, 40)
	return market.Result{
		Opportunities: []market.Opportunity{
			marketOpp(riser, 10, 15), // profit +5
			marketOpp(sinker, 10, 8), // liquidity 80%
		},
		Comps: []market.Comp{
			marketComp(riser, 10, 8),
			marketComp(sinker, 10, 8),
			marketComp(brainstorm, 5, 4),
		},
		Compared: 3,
	}
}

// onMarket puts the model on the market view with every seeded row visible.
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

// marketNames is the names on the two Kind tables, in render order.
func marketNames(rows []market.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Card.Name)
	}
	return out
}

// compNames is its twin for the comp sheet.
func compNames(rows []market.Comp) []string {
	out := make([]string, 0, len(rows))
	for _, c := range rows {
		out = append(out, c.Card.Name)
	}
	return out
}

// THE BUG: `/` on the market view opens the bar and the query parses, but
// nothing consumes it when the tables are built — so neither Kind table nor
// the comp sheet narrows the way All Cards does.
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

	// Rendered, not just held: the pane on screen is what the owner reads.
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

// The comp sheet is a different data source from the two Kind tables, and a
// card the Kind tables never list still answers the query on its own.
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

// The grammar a market row can answer: name, set and finish read off the
// printing, and qty and value are the copies held and what hoard says they
// are worth — the same numbers the M floor already filters this screen by.
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

// A trait term is answered by the catalog id set, which both an opportunity
// and a comp sheet carry a scryfall id for — so the trait half of the
// grammar works unchanged across all three tables.
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

// price is the term this screen refuses. A market row carries four of them —
// the sales anchor, the low ask, the buylist bid and the ratio between two
// of those — and the screen exists for the gap between them, so picking one
// would be silently wrong for the rest. The bar names the refusal.
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

// The refusal outlives the bar. With the bar closed, three tables reading
// "none match price<5" would claim no card on this screen costs under five
// dollars — a lie about the hoard, since the term was declined and never
// answered. The refusal takes the rung above the query in both the tables'
// empty note and the status line.
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

// board is refused for the reason movers refuses it: an owned printing sums
// every holding of that card and finish, so no row has one board.
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

// A market table emptied by the query says so. "nothing today" would be a
// lie about the hoard when a typed query is what emptied it, and it would
// send the reader to F to refetch prices they already have.
func TestMarketEmptiedByFilterReadsAsFiltered(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "zzz")
	if len(m.marketRows) != 0 || len(m.marketComps) != 0 {
		t.Fatalf("market = %v / %v, want nothing to match",
			marketNames(m.marketRows), compNames(m.marketComps))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // close the bar; the query stays
	m = next.(Model)
	out := m.View()
	if strings.Contains(out, "nothing today") {
		t.Errorf("an emptied-by-filter market must not blame the vendors:\n%s", out)
	}
	if !strings.Contains(out, "none match zzz") {
		t.Errorf("an emptied-by-filter market must say it is filtered:\n%s", out)
	}
	// The tables keep their headings, so the screen reads as filtered rather
	// than as one that failed to draw.
	if !strings.Contains(out, market.CompsTitle) {
		t.Errorf("the emptied tables must keep their headings:\n%s", out)
	}
}

// esc clears the query and restores every row, as on All Cards.
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

// The query survives closing the bar with enter — the bar edits the query,
// it is not the query.
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

// With the bar closed, the status line is the only thing on screen saying
// the tables are showing a subset — and it has to survive the clamp on an
// ordinary terminal, not just exist in the string before it is fitted.
func TestMarketStatusNamesTheLiveFilter(t *testing.T) {
	m := onMarket(t, testStore())
	m = typeFilter(m, "ris")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // close the bar; the query stays
	m = next.(Model)

	if m.mode() == modeFilter {
		t.Fatal("enter must close the bar")
	}
	if out := m.View(); !strings.Contains(out, "filtered by ris") {
		t.Errorf("the status line must name the live query:\n%s", out)
	}
}

// The bar counts what the query selected, over the whole result rather than
// the page — market pages at 50 a table, and "50 match" on every page of a
// 300-row result would read as the answer.
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

// The count speaks for the full ranking, not the page on screen.
func TestMarketFilterMatchCountIsTheWholeResult(t *testing.T) {
	m := onMarket(t, testStore())
	// More comps than one page holds, all matching the same query.
	var comps []market.Comp
	for i := range pageSize + 10 {
		comps = append(comps, marketComp(
			marketCard("Fixture", "aaa", "nonfoil", 1, float64(100-i)), 10, 8))
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

// assertSameNames compares two name lists as sets — the tables carry their
// own orders, and this is about membership.
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
