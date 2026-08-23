package browse

import (
	"errors"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

func mover(scryfallID string, fin finish.Finish, copies int, old, now float64) store.PriceChange {
	return store.PriceChange{
		ScryfallID: scryfallID, Name: strings.TrimSuffix(scryfallID, "-id"),
		SetCode: "uma", CollectorNumber: "1", Finish: fin,
		Copies: copies, Old: old, New: now,
	}
}

func TestMoversFilterByContainer(t *testing.T) {
	st := testStore()
	st.deckCards[201] = append(st.deckCards[201], entry("Etched Card", "main", finish.Etched, 1, 5))
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
		mover("Solitude-id", finish.Nonfoil, 1, 30, 34),
		mover("Force of Will-id", finish.Foil, 2, 40, 45),
		mover("Etched Card-id", finish.Foil, 1, 4, 5),
	}
	m := newTestModel(t, st)

	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Fatalf("binder movers = %+v, want Bitterblossom alone", m.movers)
	}
	if title, _ := m.viewHeader(); !strings.Contains(title, "· BINDER") {
		t.Errorf("header = %q, want the selection named", title)
	}

	m = key(m, "tab")
	m = key(m, "down")
	if len(m.movers) != 2 || m.movers[0].Name != "Force of Will" {
		t.Fatalf("rich deck movers = %+v, want its two cards, biggest impact first", m.movers)
	}

	m = key(m, "down")
	if len(m.movers) != 1 || m.movers[0].Name != "Etched Card" {
		t.Fatalf("cheap deck movers = %+v, want the foil-priced mover matching the etched holding", m.movers)
	}

	m = key(m, "home")
	if len(m.movers) != 4 {
		t.Errorf("all-cards movers = %d rows, want all 4", len(m.movers))
	}
	if title, _ := m.viewHeader(); strings.Contains(title, "· ALL CARDS") {
		t.Errorf("header = %q, All Cards is the unfiltered whole, not a scope", title)
	}
}

func TestUnpricedGreysAndSkipsIneligibleContainers(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{
		{ScryfallID: "Solitude-id", Name: "Solitude", SetCode: "mh3", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 1, HeldIn: "Rich Deck"},
	}
	m := newTestModel(t, st)
	m = key(m, "v")
	_ = (&m).showView(viewWatches)

	if m.cursor[paneContainers] != 0 {
		t.Fatalf("cursor = %d, want the ineligible selection snapped to All Cards", m.cursor[paneContainers])
	}
	if !strings.Contains(m.status, "has no watches") {
		t.Errorf("status = %q, want the snap explained", m.status)
	}
	if len(m.unpriced) != 1 {
		t.Fatalf("all-cards unpriced = %d rows, want 1", len(m.unpriced))
	}
	if m.containerEligible(1) || !m.containerEligible(2) || m.containerEligible(3) {
		t.Errorf("eligibility = binder %v, rich %v, cheap %v; want only Rich Deck",
			m.containerEligible(1), m.containerEligible(2), m.containerEligible(3))
	}

	m = key(m, "tab")
	m = key(m, "down")
	if m.cursor[paneContainers] != 2 {
		t.Fatalf("cursor = %d, want the greyed binder skipped", m.cursor[paneContainers])
	}
	if len(m.unpriced) != 1 || m.unpriced[0].Name != "Solitude" {
		t.Errorf("rich deck unpriced = %+v", m.unpriced)
	}

	m = key(m, "down")
	if m.cursor[paneContainers] != 2 {
		t.Errorf("cursor = %d, want to stay with no eligible row below", m.cursor[paneContainers])
	}
	m = key(m, "end")
	if m.cursor[paneContainers] != 2 {
		t.Errorf("end landed on %d, want the last eligible row", m.cursor[paneContainers])
	}
	m = key(m, "up")
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want All Cards", m.cursor[paneContainers])
	}
}

func TestWatchesFilterByContainer(t *testing.T) {
	st := testStore()
	w1 := store.WatchStatus{Name: "Bitterblossom", PriceUSD: price(34)}
	w1.ScryfallID, w1.Finish, w1.Op, w1.Threshold = "Bitterblossom-id", finish.Nonfoil, "under", 30
	w2 := store.WatchStatus{Name: "Force of Will", PriceUSD: price(45)}
	w2.ScryfallID, w2.Finish, w2.Op, w2.Threshold = "Force of Will-id", finish.Foil, "over", 50
	st.watches = []store.WatchStatus{w1, w2}

	m := atAllCards(t, newTestModel(t, st))
	for range 4 {
		m = key(m, "v")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	if len(shownWatches(m)) != 2 {
		t.Fatalf("all-cards watches = %d, want both", len(shownWatches(m)))
	}

	m = key(m, "tab")
	m = key(m, "down")

	if m.cursor[paneContainers] != 1 || len(m.unders) != 1 || len(m.overs) != 0 ||
		m.unders[0].Name != "Bitterblossom" {
		t.Fatalf("binder watches = %+v/%+v at cursor %d", m.overs, m.unders, m.cursor[paneContainers])
	}
	if title, _ := m.viewHeader(); !strings.Contains(title, "WATCHES · BINDER") {
		t.Errorf("header = %q, want the selection named", title)
	}
	m = key(m, "down")
	if len(m.overs) != 1 || len(m.unders) != 0 || m.overs[0].Name != "Force of Will" {
		t.Fatalf("rich deck watches = %+v/%+v", m.overs, m.unders)
	}
	m = key(m, "down")
	if m.cursor[paneContainers] != 2 {
		t.Errorf("cursor = %d, want the watchless deck skipped", m.cursor[paneContainers])
	}
}

func TestMarketRowsFollowContainer(t *testing.T) {
	bitter := opp("Bitter", 2, 20)
	bitter.Card.ScryfallID = "Bitterblossom-id"
	sol := opp("Sol", 2, 20)
	sol.Card.ScryfallID = "Solitude-id"
	bc := comp("BitterC", 60, 55, 44)
	bc.Card.ScryfallID = "Bitterblossom-id"
	sc := comp("SolC", 50, 45, 40)
	sc.Card.ScryfallID = "Solitude-id"
	res := market.Result{
		Opportunities: []market.Opportunity{bitter, sol},
		Comps:         []market.Comp{bc, sc},
		Compared:      4,
	}

	m := newTestModel(t, testStore())
	m.view = viewMarket
	m.marketResult = res
	m.marketLoaded = true
	m.applyMarketRows()

	if len(m.marketRows) != 1 || m.marketRows[0].Card.Name != "Bitter" {
		t.Fatalf("binder market rows = %+v, want Bitterblossom's alone", m.marketRows)
	}
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Name != "BitterC" {
		t.Fatalf("binder comps = %+v, want Bitterblossom's alone", m.marketComps)
	}
	if title, _ := m.marketHeader(); !strings.Contains(title, "MARKET · BINDER") {
		t.Errorf("header = %q, want the selection named", title)
	}

	m.focus = paneContainers
	m.move(1)
	if len(m.marketRows) != 1 || m.marketRows[0].Card.Name != "Sol" {
		t.Fatalf("rich deck market rows = %+v", m.marketRows)
	}
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Name != "SolC" {
		t.Fatalf("rich deck comps = %+v", m.marketComps)
	}

	m.moveTo(0)
	if len(m.marketRows) != 2 || len(m.marketComps) != 2 {
		t.Errorf("all-cards market = %d rows, %d comps, want 2 and 2",
			len(m.marketRows), len(m.marketComps))
	}
}

func TestMarketSectionRegionsScroll(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for _, n := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		m.marketRows = append(m.marketRows, market.Row{Kind: market.KindProfit, Opportunity: opp(n, 1, 10)})
	}
	m.marketComps = []market.Comp{
		comp("C1", 60, 55, 44), comp("C2", 50, 45, 40), comp("C3", 40, 35, 30),
		comp("C4", 30, 25, 20), comp("C5", 20, 15, 10),
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 22})
	m = next.(Model)
	if got := m.marketSectionBudgets(); got != [3]int{4, 0, 4} {
		t.Fatalf("budgets = %v, want [4 0 4]", got)
	}

	view := strings.Join(m.marketLines(110), "\n")
	if !strings.Contains(view, "1–4 of 6") || !strings.Contains(view, "1–4 of 5") {
		t.Fatalf("overflow positions missing:\n%s", view)
	}
	if strings.Count(view, "nothing today") != 1 {
		t.Errorf("the empty section should keep its title over a note:\n%s", view)
	}
	if !strings.Contains(view, "P1") || strings.Contains(view, "P5") {
		t.Errorf("profit region should show rows 1–4 only:\n%s", view)
	}

	for range 4 {
		m = key(m, "down")
	}
	if m.marketSecOffset != [3]int{1, 0, 0} {
		t.Fatalf("offsets = %v, want the profit section scrolled once", m.marketSecOffset)
	}
	view = strings.Join(m.marketLines(110), "\n")
	if !strings.Contains(view, "2–5 of 6") || !strings.Contains(view, "C1") {
		t.Errorf("profit scrolled without disturbing comps:\n%s", view)
	}

	for range 7 {
		m = key(m, "down")
	}
	if sec, idx := m.marketCursorPos(); sec != compsSection || idx != 4 {
		t.Fatalf("cursor at (%d,%d), want the last comp", sec, idx)
	}
	if m.marketSecOffset[compsSection] != 1 {
		t.Errorf("comps offset = %d, want 1", m.marketSecOffset[compsSection])
	}

	m.marketComps = m.marketComps[:1]
	m.cursor[paneCards] = 0
	m.marketSecOffset = [3]int{}
	if got := m.marketSectionBudgets(); got != [3]int{6, 0, 1} {
		t.Errorf("budgets = %v, want the slack donated to the overfull table", got)
	}
}

func TestCompsSidesToggle(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	m.marketAllComps = []market.Comp{comp("Sheeted", 60, 55, 44)}
	m.deriveMarketPages()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(Model)

	m.cursor[paneCards] = m.marketSections()[compsSection].curStart

	compsPart := func(view string) string {
		if i := strings.Index(view, "COMPS"); i >= 0 {
			return view[i:]
		}
		return view
	}
	view := compsPart(strings.Join(m.marketLines(110), "\n"))
	if !strings.Contains(view, "COMPS · SELL") || !strings.Contains(view, "TCG SOLD") ||
		!strings.Contains(view, "PRICE DISPERSION") {
		t.Fatalf("default side should comp the sale prices with their dispersion:\n%s", view)
	}

	if strings.Contains(view, "SPREAD") || strings.Contains(view, "BUYLIST") ||
		strings.Contains(view, "LOW") {
		t.Errorf("the bid vocabulary belongs to the buy side:\n%s", view)
	}
	note := m.selectedMarketNote()
	if !strings.Contains(note, "last sold") || strings.Contains(note, "pays") {
		t.Errorf("sell note = %q, want the sale prices without the bid", note)
	}

	m = key(m, "b")
	if !m.compsBuySide {
		t.Fatal("b did not flip to the buy side")
	}
	view = compsPart(strings.Join(m.marketLines(110), "\n"))
	if !strings.Contains(view, "COMPS · BUY") || !strings.Contains(view, "CK BUYLIST") ||
		!strings.Contains(view, "SPREAD") || !strings.Contains(view, "TCG SOLD") {
		t.Fatalf("buy side should anchor the bid against tcg sold:\n%s", view)
	}
	if strings.Contains(view, "AT ") || strings.Contains(view, "LOW") {
		t.Errorf("the cheapest-ask columns are gone:\n%s", view)
	}
	if note := m.selectedMarketNote(); !strings.Contains(note, "pays") {
		t.Errorf("buy note = %q, want the bid explained", note)
	}
	m = key(m, "b")
	if m.compsBuySide {
		t.Error("b must toggle back to the sell side")
	}
}

func TestMarketStatusCountsPerTable(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for _, n := range []string{"P1", "P2", "P3"} {
		m.marketRows = append(m.marketRows, market.Row{Kind: market.KindProfit, Opportunity: opp(n, 1, 10)})
	}
	m.marketComps = []market.Comp{comp("C1", 60, 55, 44), comp("C2", 50, 45, 40)}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(Model)

	m.cursor[paneCards] = 1
	if got := m.marketStatus(); !strings.Contains(got, "2/3") {
		t.Errorf("status = %q, want 2/3 within the profit table", got)
	}
	m.cursor[paneCards] = m.marketSections()[compsSection].curStart
	if got := m.marketStatus(); !strings.Contains(got, "1/2") {
		t.Errorf("status = %q, want 1/2 within the comps", got)
	}

	if got := m.marketStatus(); !strings.Contains(got, "PRICE DISPERSION = high sale minus low sale") {
		t.Errorf("status = %q, want the dispersion formula on the sell side", got)
	}
	m.compsBuySide = true
	if got := m.marketStatus(); !strings.Contains(got, "SPREAD = 1 − BUYLIST ÷ LOW") {
		t.Errorf("status = %q, want the bid-spread formula on the buy side", got)
	}
	m.compsBuySide = false
}

func TestMarketTableJumpKeys(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for _, n := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		m.marketRows = append(m.marketRows, market.Row{Kind: market.KindProfit, Opportunity: opp(n, 1, 10)})
	}
	m.marketComps = []market.Comp{comp("C1", 60, 55, 44), comp("C2", 50, 45, 40)}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 21})
	m = next.(Model)

	secs := m.marketSections()
	m = key(m, "down")
	m = key(m, "]")
	if m.cursor[paneCards] != secs[market.KindLiquid].curStart {
		t.Fatalf("cursor = %d, want the empty buylist table's heading", m.cursor[paneCards])
	}
	m = key(m, "]")
	if m.cursor[paneCards] != secs[compsSection].curStart {
		t.Fatalf("cursor = %d, want the comps section's first row", m.cursor[paneCards])
	}
	m = key(m, "]")
	if m.cursor[paneCards] != secs[compsSection].curStart {
		t.Errorf("cursor = %d, want to clamp at the last table", m.cursor[paneCards])
	}
	m = key(m, "[")
	if m.cursor[paneCards] != secs[market.KindLiquid].curStart {
		t.Fatalf("cursor = %d, want the empty buylist table's heading", m.cursor[paneCards])
	}
	m = key(m, "[")
	if m.cursor[paneCards] != 0 {
		t.Fatalf("cursor = %d, want the profit table's first row", m.cursor[paneCards])
	}
	m = key(m, "[")
	if m.cursor[paneCards] != 0 {
		t.Errorf("cursor = %d, want to clamp at the first table", m.cursor[paneCards])
	}

	m.focus = paneContainers
	m = key(m, "]")
	if m.focus != paneCards || m.cursor[paneCards] != len(m.marketRows) {
		t.Errorf("jump from the container pane: focus %v, cursor %d", m.focus, m.cursor[paneCards])
	}
}

func TestCycleFloorKeepsPristine(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
		mover("Sol Ring-id", finish.Nonfoil, 1, 1, 2),
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 2 {
		t.Fatalf("movers = %d, want 2 before the floor", len(m.movers))
	}

	st.err = errors.New("the floor must not re-query")
	m = key(m, "M")
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Fatalf("floored movers = %+v", m.movers)
	}
	for range len(floorLevels) - 1 {
		m = key(m, "M")
	}
	if len(m.movers) != 2 {
		t.Errorf("movers = %d after the floor lifted, want the hidden row back", len(m.movers))
	}
	if m.statusErr {
		t.Errorf("cycling the floor touched the store: %q", m.status)
	}
}

func TestReloadRederivesView(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 1 {
		t.Fatalf("movers = %d, want 1", len(m.movers))
	}
	st.movers = append(st.movers, mover("Solitude-id", finish.Nonfoil, 1, 30, 34))
	m = key(m, "r")
	if len(m.movers) != 2 {
		t.Errorf("movers = %d after reload, want the new row", len(m.movers))
	}
	if m.cursor[paneContainers] != 0 {
		t.Errorf("reload moved the container cursor to %d", m.cursor[paneContainers])
	}
}

func TestMoversWindowCacheMakesWInstant(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	m = key(m, "W")
	m = key(m, "W")

	st.err = errors.New("the cache must answer")
	m = key(m, "W")
	if m.statusErr {
		t.Fatalf("cached window hit the store: %q", m.status)
	}
	if len(m.movers) != 1 {
		t.Fatalf("cached movers = %d rows, want 1", len(m.movers))
	}
	st.err = nil

	st.movers = append(st.movers, mover("Solitude-id", finish.Nonfoil, 1, 30, 34))
	m = key(m, "r")
	if len(m.movers) != 2 {
		t.Fatalf("reloaded movers = %d rows, want the new row visible", len(m.movers))
	}
	m = key(m, "W")
	if len(m.movers) != 2 {
		t.Errorf("post-reload window = %d rows, want re-queried, not stale cache", len(m.movers))
	}
}

func TestMoversPennyGate(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
		mover("Sol Ring-id", finish.Nonfoil, 1, 0.15, 0.18),
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Fatalf("default movers = %+v, want the penny row hidden", m.movers)
	}
	if m.status != "showing movers · sorted by impact · penny filter ≤ $0.50" {
		t.Errorf("arrival beat = %q, want the sort and the armed filter named", m.status)
	}
	m.status = ""
	if !strings.Contains(m.View(), "penny filter ≤ $0.50") {
		t.Error("the default gate must announce itself on the status line")
	}

	m.moversPennies = true
	m.deriveView()
	if len(m.movers) != 2 {
		t.Fatalf("with pennies shown movers = %d rows, want both", len(m.movers))
	}

	m.floorIdx = 1
	m.deriveView()
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Errorf("floor over pennies = %+v, want only the $34 card", m.movers)
	}
}

func TestSetPennyFilter(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
		mover("Sol Ring-id", finish.Nonfoil, 1, 0.15, 0.18),
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")

	m.promptSetPennyLimit()
	if m.prompt == nil {
		t.Fatal("SetPennyFilter must open a prompt")
	}
	if m.prompt.text != "0.5" {
		t.Errorf("prompt prefill = %q, want the current limit", m.prompt.text)
	}
	for _, bad := range []string{"", "abc", "-1", "101", "NaN"} {
		if err := m.prompt.validate(bad); err == nil {
			t.Errorf("validate(%q) accepted, want refused", bad)
		}
	}
	if err := m.prompt.validate("$1.50"); err != nil {
		t.Errorf("validate($1.50) = %v, want accepted", err)
	}

	m.prompt.commit(&m, "$40")
	if len(m.movers) != 0 {
		t.Errorf("limit $40 movers = %+v, want all gated", m.movers)
	}
	if !strings.Contains(m.status, "$40") {
		t.Errorf("status = %q, want the new limit named", m.status)
	}

	m.promptSetPennyLimit()
	m.prompt.commit(&m, "0.10")
	m.prompt = nil
	if len(m.movers) != 2 {
		t.Errorf("limit $0.10 movers = %d rows, want both", len(m.movers))
	}
	m.status = ""
	if !strings.Contains(m.View(), "penny filter ≤ $0.10") {
		t.Errorf("status hint must track the moved line")
	}

	m.moversPennies = true
	m.promptSetPennyLimit()
	m.prompt.commit(&m, "0.20")
	if m.moversPennies {
		t.Error("committing a new line must re-arm the gate")
	}
}

func TestViewCycleKeepsGutterSteady(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	frame := func() (total, rule int) {
		lines := strings.Split(m.View(), "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "───") {
				return len(lines), i
			}
		}
		t.Fatalf("no rule line in frame:\n%s", m.View())
		return 0, 0
	}
	wantTotal, wantRule := frame()
	for range 5 {
		m = key(m, "v")
		total, rule := frame()
		if total != wantTotal || rule != wantRule {
			t.Errorf("view %v: frame %d lines with the rule at %d, want %d/%d",
				m.view, total, rule, wantTotal, wantRule)
		}
	}
}

func TestScopedViewsShowContainerQuantity(t *testing.T) {
	st := testStore()

	st.deckCards[201] = append(st.deckCards[201], entry("Solitude", "main", finish.Nonfoil, 3, 34))
	st.movers = []store.PriceChange{mover("Solitude-id", finish.Nonfoil, 4, 30, 34)}

	m := newTestModel(t, st)
	m = key(m, "v")
	if len(m.movers) != 0 {
		t.Fatalf("binder movers = %+v, want none (Solitude lives in decks)", m.movers)
	}
	m = key(m, "tab")
	m = key(m, "down")
	if len(m.movers) != 1 || m.movers[0].Copies != 1 {
		t.Fatalf("rich deck movers = %+v, want Solitude at the deck's 1 copy", m.movers)
	}
	m = key(m, "down")
	if len(m.movers) != 1 || m.movers[0].Copies != 3 {
		t.Fatalf("cheap deck movers = %+v, want Solitude at the deck's 3 copies", m.movers)
	}
	m = key(m, "home")
	if len(m.movers) != 1 || m.movers[0].Copies != 4 {
		t.Fatalf("all-cards movers = %+v, want the hoard-wide 4 copies", m.movers)
	}

	sc := comp("SolC", 136, 34, 30)
	sc.Card.ScryfallID = "Solitude-id"
	sc.Card.Copies = 4
	m.view = viewMarket
	m.marketResult = market.Result{Comps: []market.Comp{sc}, Compared: 1}
	m.marketLoaded = true
	m.focus = paneContainers
	m.moveTo(0)
	m.move(2)
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Copies != 1 {
		t.Fatalf("rich deck comps = %+v, want the deck's single copy", m.marketComps)
	}
	if v := m.marketComps[0].Card.Value; v != 34 {
		t.Errorf("scoped comp value = %v, want 34 (one copy's worth)", v)
	}
	m.moveTo(0)
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Copies != 4 || m.marketComps[0].Card.Value != 136 {
		t.Fatalf("all-cards comps = %+v, want the hoard-wide figures restored", m.marketComps)
	}
}

func TestCursorMovementRestoresStatusLine(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for _, n := range []string{"P1", "P2"} {
		m.marketRows = append(m.marketRows, market.Row{Kind: market.KindProfit, Opportunity: opp(n, 1, 10)})
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(Model)

	m.status = "sorted by arbitrage · profit"
	if got := m.statusLine(); !strings.Contains(got, "sorted by arbitrage") {
		t.Fatalf("status = %q, want the receipt while it is fresh", got)
	}
	m = key(m, "down")
	if got := m.statusLine(); !strings.Contains(got, "2/2") || !strings.Contains(got, "pays") {
		t.Errorf("status after moving = %q, want the selected row's market note", got)
	}
}

func TestMarketPennyFilterReCollects(t *testing.T) {
	cheap := comp("Bulk", 0.30, 0.30, 0.10)
	mid := comp("Mid", 0.50, 0.50, 0.30)
	dear := comp("Dear", 60, 55, 44)
	cacheAlive := true
	m, err := New(testStore(), WithMarketCached(func(min float64) (market.Result, bool) {
		if !cacheAlive {
			return market.Result{}, false
		}
		var res market.Result
		for _, c := range []market.Comp{cheap, mid, dear} {
			if c.Low >= min {
				res.Comps = append(res.Comps, c)
			}
		}
		res.Compared = len(res.Comps)
		return res, true
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(Model)
	m = atAllCards(t, m)
	runByID := func(m Model, id string) Model {
		t.Helper()
		for _, c := range m.commands {
			if c.id == id {
				c.run(&m)
				return m
			}
		}
		t.Fatalf("no command %q", id)
		return m
	}
	for m.view != viewMarket {
		m = key(m, "v")
	}
	if !m.marketLoaded || len(m.marketComps) != 1 {
		t.Fatalf("comps at the $1 default = %+v, want Dear alone", m.marketComps)
	}
	if !strings.Contains(m.status, "penny filter < $1.00") {
		t.Errorf("arrival beat = %q, want the armed floor named", m.status)
	}

	m = runByID(m, "market.pennies")
	if len(m.marketComps) != 3 {
		t.Fatalf("comps with the floor off = %+v, want all three", m.marketComps)
	}
	if !strings.Contains(m.status, "penny filter < $1.00 off") {
		t.Errorf("status = %q, want the toggle receipt", m.status)
	}
	m = runByID(m, "market.pennies")
	if len(m.marketComps) != 1 || !strings.Contains(m.status, "penny filter < $1.00 on") {
		t.Fatalf("re-armed comps = %+v status %q, want the floor back", m.marketComps, m.status)
	}

	m.promptSetMarketFloor()
	if m.prompt == nil || m.prompt.text != "1" {
		t.Fatalf("prompt = %+v, want prefilled with the current floor", m.prompt)
	}
	if err := m.prompt.validate("101"); err == nil {
		t.Error("validate(101) accepted, want refused")
	}
	m.prompt.commit(&m, "0.40")
	m.prompt = nil
	if len(m.marketComps) != 2 {
		t.Fatalf("comps at $0.40 = %+v, want Mid and Dear", m.marketComps)
	}
	if m.marketFloor != 0.40 || !strings.Contains(m.status, "penny filter < $0.40 on") {
		t.Errorf("floor = %v status %q, want the moved line named", m.marketFloor, m.status)
	}

	cacheAlive = false
	m = runByID(m, "market.pennies")
	if m.marketLoaded || !strings.Contains(m.status, "press F") {
		t.Errorf("loaded = %v status %q, want a fresh-fetch ask", m.marketLoaded, m.status)
	}
}

func TestMarketTablesPage(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for i := range 60 {
		m.marketAllRows = append(m.marketAllRows,
			market.Row{Kind: market.KindProfit, Opportunity: opp(fmt.Sprintf("P%03d", i), 1, 10)})
	}
	for i := range 120 {
		m.marketAllComps = append(m.marketAllComps, comp(fmt.Sprintf("C%03d", i), 60, 55, 44))
	}
	m.deriveMarketPages()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	if len(m.marketRows) != 50 || len(m.marketComps) != 50 {
		t.Fatalf("visible rows = %d + %d comps, want 50 each", len(m.marketRows), len(m.marketComps))
	}
	if view := strings.Join(m.marketLines(120), "\n"); !strings.Contains(view, " · 1–50 of 60") ||
		!strings.Contains(view, " · 1–50 of 120") {
		t.Fatalf("titles must name each page's slice of the ranking:\n%s", view)
	}

	m = key(m, ">")
	if len(m.marketRows) != 10 || m.marketRows[0].Card.Name != "P050" {
		t.Fatalf("page 2 rows = %d starting %q, want the ranking's tail", len(m.marketRows), m.marketRows[0].Card.Name)
	}
	if view := strings.Join(m.marketLines(120), "\n"); !strings.Contains(view, " · 51–60 of 60") {
		t.Fatalf("page 2 must name its slice:\n%s", view)
	}
	if !strings.Contains(m.status, "page 2/2 · rows 51–60 of 60") {
		t.Errorf("status = %q, want the page named", m.status)
	}
	m = key(m, ">")
	if !strings.Contains(m.status, "last page") {
		t.Errorf("status = %q, want the far edge named", m.status)
	}
	m = key(m, "<")
	if m.marketRows[0].Card.Name != "P000" || !strings.Contains(m.status, "page 1/2") {
		t.Fatalf("back to page 1 = %q status %q", m.marketRows[0].Card.Name, m.status)
	}

	m = key(m, "]")
	m = key(m, "]")
	m = key(m, ">")
	m = key(m, ">")
	if len(m.marketComps) != 20 || m.marketComps[0].Card.Name != "C100" {
		t.Fatalf("comps page 3 = %d rows starting %q, want the last twenty", len(m.marketComps), m.marketComps[0].Card.Name)
	}
	if m.cursor[paneCards] != m.marketSections()[compsSection].curStart {
		t.Errorf("cursor = %d, want the comps section's first row", m.cursor[paneCards])
	}

	m = key(m, "s")
	if m.marketPage[compsSection] != 0 || len(m.marketComps) != 50 {
		t.Errorf("after sorting page = %d with %d rows, want page one full", m.marketPage[compsSection], len(m.marketComps))
	}
}

func TestStatusLineLeadsWithPosition(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m.status = ""
	if got := m.statusLine(); !strings.Contains(got, "1/4 · All Cards") {
		t.Errorf("container status = %q, want position then the selected container", got)
	}

	m = key(m, "tab")
	if got := m.statusLine(); !strings.Contains(got, "1/6 · Force of Will") {
		t.Errorf("holdings status = %q, want position then the selected card", got)
	}
	m = key(m, "down")
	if got := m.statusLine(); !strings.Contains(got, "2/6 · Bitterblossom") {
		t.Errorf("moved status = %q, want position then the new selection", got)
	}

	m = key(m, "v")
	m.status = ""
	if got := m.statusLine(); !strings.Contains(got, "1/1 · Bitterblossom") {
		t.Errorf("movers status = %q, want position then the mover's name", got)
	}

	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	m.marketAllComps = []market.Comp{comp("SolC", 50, 45, 40)}
	m.deriveMarketPages()
	m.cursor[paneCards] = m.marketSections()[compsSection].curStart
	if got := m.marketStatus(); !strings.Contains(got, "SolC · 1/1") {
		t.Errorf("market status = %q, want the comp's card leading", got)
	}
}

func TestPennyFiltersPersist(t *testing.T) {
	st := testStore()
	m := atAllCards(t, newTestModel(t, st))

	m.promptSetPennyLimit()
	m.prompt.commit(&m, "0.55")
	m.prompt = nil
	m.marketPennies = true
	m.persistPennyFilters()

	if st.settings[setMoversPennyLine] != "0.55" || st.settings[setMarketPennies] != "true" {
		t.Fatalf("stored = %v, want both changes written", st.settings)
	}

	m2 := newTestModel(t, st)
	if m2.moversPennyLimit != 0.55 || !m2.marketPennies {
		t.Errorf("restored limit %v pennies %v, want the stored session back",
			m2.moversPennyLimit, m2.marketPennies)
	}
	if m2.marketFloor != 1.00 {
		t.Errorf("market floor = %v, want the default where nothing was stored differently", m2.marketFloor)
	}

	st.settings[setMarketFloor] = "over 9000"
	st.settings[setMoversPennyLine] = "-3"
	m3 := newTestModel(t, st)
	if m3.marketFloor != 1.00 || m3.moversPennyLimit != defaultPennyLimit {
		t.Errorf("garbled settings restored as floor %v limit %v, want the defaults",
			m3.marketFloor, m3.moversPennyLimit)
	}
}

func TestHoldingsTablePages(t *testing.T) {
	st := testStore()
	for i := range 80 {
		st.collection = append(st.collection,
			row(fmt.Sprintf("Filler %02d", i), "set", fmt.Sprint(i), finish.Nonfoil, 1, float64(10000-i)))
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	total := len(m.filteredCards)
	if len(m.cards) != singleTablePageSize || total <= singleTablePageSize {
		t.Fatalf("page 1 = %d rows of %d, want %d of more", len(m.cards), total, singleTablePageSize)
	}
	if _, totals := m.viewHeader(); !strings.Contains(totals, fmt.Sprintf(" · 1–%d of %d", singleTablePageSize, total)) {
		t.Fatalf("header totals = %q, want the page slice named", totals)
	}

	m = key(m, ">")
	first := fmt.Sprintf("Filler %02d", singleTablePageSize)
	if m.cards[0].Name != first || m.cursor[paneCards] != 0 {
		t.Fatalf("page 2 starts %q at cursor %d, want %s at 0", m.cards[0].Name, m.cursor[paneCards], first)
	}
	if want := fmt.Sprintf("page 2/2 · rows %d–%d of %d · sorted by value", singleTablePageSize+1, total, total); !strings.Contains(m.status, want) {
		t.Errorf("status = %q, want %q", m.status, want)
	}
	m = key(m, ">")
	if !strings.Contains(m.status, "last page") {
		t.Errorf("status = %q, want the far edge named", m.status)
	}

	m = key(m, "<")
	if m.cards[0].Name != "Filler 00" || !strings.Contains(m.status, "page 1/2") {
		t.Fatalf("back to page 1 = %q status %q", m.cards[0].Name, m.status)
	}

	m = key(m, ">")
	m = key(m, "+")
	if m.cardsPage != 1 {
		t.Fatalf("after an edit: page %d, want page 2 kept", m.cardsPage+1)
	}

	m = key(m, "s")
	if m.cardsPage != 0 {
		t.Errorf("page after sort = %d, want the first", m.cardsPage)
	}

	m = key(m, ">")
	m = key(m, "/")
	m = key(m, "f")
	if m.cardsPage != 0 {
		t.Errorf("page after filtering = %d, want the first", m.cardsPage)
	}
	if bar := m.statusLine(); !strings.Contains(bar, fmt.Sprintf("%d match", len(m.filteredCards))) ||
		len(m.filteredCards) <= singleTablePageSize {
		t.Errorf("filter bar = %q with %d matches; the count must cover every page", bar, len(m.filteredCards))
	}
}

func TestMoversTablePages(t *testing.T) {
	st := testStore()
	for i := range 80 {
		st.movers = append(st.movers,
			mover(fmt.Sprintf("M%02d-id", i), finish.Nonfoil, 1, 10, 10+float64(80-i)))
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	if len(m.movers) != singleTablePageSize || len(m.filteredMovers) != 80 {
		t.Fatalf("page = %d of %d, want %d of 80", len(m.movers), len(m.filteredMovers), singleTablePageSize)
	}
	if _, totals := m.viewHeader(); !strings.Contains(totals, "80 moved") ||
		!strings.Contains(totals, fmt.Sprintf(" · 1–%d of 80", singleTablePageSize)) {
		t.Fatalf("header totals = %q, want the whole ranking counted and the slice named", totals)
	}

	m = key(m, ">")
	firstMover := fmt.Sprintf("M%02d", singleTablePageSize)
	if m.movers[0].Name != firstMover ||
		!strings.Contains(m.status, fmt.Sprintf("page 2/2 · rows %d–80 of 80", singleTablePageSize+1)) {
		t.Fatalf("page 2 starts %q status %q", m.movers[0].Name, m.status)
	}

	m = key(m, "W")
	if m.moversPage != 0 {
		t.Errorf("page after W = %d, want the first", m.moversPage)
	}
}
