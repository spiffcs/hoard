package browse

// The container pane as a filter over the analytical views: membership
// joins, eligibility greying and skipping, the view-switch snap, and the
// market view's fixed-region layout.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

// mover builds one price change over a fixture-held printing.
func mover(scryfallID, finish string, copies int, old, now float64) store.PriceChange {
	return store.PriceChange{
		ScryfallID: scryfallID, Name: strings.TrimSuffix(scryfallID, "-id"),
		SetCode: "uma", CollectorNumber: "1", Finish: finish,
		Copies: copies, Old: old, New: now,
	}
}

// The movers view narrows to the selected container: the binder shows its
// own movers, a deck its own, All cards everything — and a foil-priced
// mover matches a container that holds only the etched printing, because
// price feeds fold etched into foil.
func TestMoversFilterByContainer(t *testing.T) {
	st := testStore()
	st.deckCards[201] = append(st.deckCards[201], entry("Etched Card", "main", "etched", 1, 5))
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", "nonfoil", 4, 30, 34),
		mover("Solitude-id", "nonfoil", 1, 30, 34),
		mover("Force of Will-id", "foil", 2, 40, 45),
		mover("Etched Card-id", "foil", 1, 4, 5),
	}
	m := newTestModel(t, st) // the default binder is selected

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

	m = key(m, "tab")  // into the container pane
	m = key(m, "down") // Rich Deck
	if len(m.movers) != 2 || m.movers[0].Name != "Force of Will" {
		t.Fatalf("rich deck movers = %+v, want its two cards, biggest impact first", m.movers)
	}

	m = key(m, "down") // Cheap Deck: holds the etched printing of a foil-priced mover
	if len(m.movers) != 1 || m.movers[0].Name != "Etched Card" {
		t.Fatalf("cheap deck movers = %+v, want the foil-priced mover matching the etched holding", m.movers)
	}

	m = key(m, "home") // All cards
	if len(m.movers) != 4 {
		t.Errorf("all-cards movers = %d rows, want all 4", len(m.movers))
	}
	if title, _ := m.viewHeader(); strings.Contains(title, "· ALL CARDS") {
		t.Errorf("header = %q, All cards is the unfiltered whole, not a scope", title)
	}
}

// The unpriced view greys out containers with nothing unpriced: switching
// to it from an ineligible selection snaps to All cards, and the container
// cursor steps over the greyed rows in both directions.
func TestUnpricedGreysAndSkipsIneligibleContainers(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{
		{ScryfallID: "Solitude-id", Name: "Solitude", SetCode: "mh3", CollectorNumber: "1",
			Finish: "nonfoil", Copies: 1, HeldIn: "Rich Deck"},
	}
	m := newTestModel(t, st) // binder selected — no unpriced card lives there
	m = key(m, "v")          // movers
	m = key(m, "v")          // unpriced

	if m.cursor[paneContainers] != 0 {
		t.Fatalf("cursor = %d, want the ineligible selection snapped to All cards", m.cursor[paneContainers])
	}
	if !strings.Contains(m.status, "has no unpriced") {
		t.Errorf("status = %q, want the snap explained", m.status)
	}
	if len(m.unpriced) != 1 {
		t.Fatalf("all-cards unpriced = %d rows, want 1", len(m.unpriced))
	}
	if m.containerEligible(1) || !m.containerEligible(2) || m.containerEligible(3) {
		t.Errorf("eligibility = binder %v, rich %v, cheap %v; want only Rich Deck",
			m.containerEligible(1), m.containerEligible(2), m.containerEligible(3))
	}

	m = key(m, "tab")  // into the container pane, at All cards
	m = key(m, "down") // skips the binder, lands on Rich Deck
	if m.cursor[paneContainers] != 2 {
		t.Fatalf("cursor = %d, want the greyed binder skipped", m.cursor[paneContainers])
	}
	if len(m.unpriced) != 1 || m.unpriced[0].Name != "Solitude" {
		t.Errorf("rich deck unpriced = %+v", m.unpriced)
	}

	m = key(m, "down") // nothing eligible below: the cursor stays
	if m.cursor[paneContainers] != 2 {
		t.Errorf("cursor = %d, want to stay with no eligible row below", m.cursor[paneContainers])
	}
	m = key(m, "end") // the last eligible row, not the last row
	if m.cursor[paneContainers] != 2 {
		t.Errorf("end landed on %d, want the last eligible row", m.cursor[paneContainers])
	}
	m = key(m, "up") // back over the binder to All cards
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want All cards", m.cursor[paneContainers])
	}
}

// The watches view filters like movers — price-finish join — and greys out
// containers holding no watched card.
func TestWatchesFilterByContainer(t *testing.T) {
	st := testStore()
	w1 := store.WatchStatus{Name: "Bitterblossom", PriceUSD: price(34)}
	w1.ScryfallID, w1.Finish, w1.Op, w1.Threshold = "Bitterblossom-id", "nonfoil", "<=", 30
	w2 := store.WatchStatus{Name: "Force of Will", PriceUSD: price(45)}
	w2.ScryfallID, w2.Finish, w2.Op, w2.Threshold = "Force of Will-id", "foil", ">=", 50
	st.watches = []store.WatchStatus{w1, w2}

	m := atAllCards(t, newTestModel(t, st))
	for range 3 {
		m = key(m, "v") // movers → unpriced → watches
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	if len(m.watches) != 2 {
		t.Fatalf("all-cards watches = %d, want both", len(m.watches))
	}

	m = key(m, "tab")
	m = key(m, "down") // the binder holds the Bitterblossom watch
	if m.cursor[paneContainers] != 1 || len(m.watches) != 1 || m.watches[0].Name != "Bitterblossom" {
		t.Fatalf("binder watches = %+v at cursor %d", m.watches, m.cursor[paneContainers])
	}
	if title, _ := m.viewHeader(); !strings.Contains(title, "WATCHES · BINDER") {
		t.Errorf("header = %q, want the selection named", title)
	}
	m = key(m, "down") // Rich Deck holds the Force of Will watch
	if len(m.watches) != 1 || m.watches[0].Name != "Force of Will" {
		t.Fatalf("rich deck watches = %+v", m.watches)
	}
	m = key(m, "down") // Cheap Deck holds no watch: greyed, skipped
	if m.cursor[paneContainers] != 2 {
		t.Errorf("cursor = %d, want the watchless deck skipped", m.cursor[paneContainers])
	}
}

// Market rows and comps follow the container, and the filter runs before
// the per-section ranking — a deck gets its own table, not the subset of
// the hoard-wide top rows that happens to mention it.
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

	m := newTestModel(t, testStore()) // binder selected
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
	m.move(1) // Rich Deck: Solitude's rows
	if len(m.marketRows) != 1 || m.marketRows[0].Card.Name != "Sol" {
		t.Fatalf("rich deck market rows = %+v", m.marketRows)
	}
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Name != "SolC" {
		t.Fatalf("rich deck comps = %+v", m.marketComps)
	}

	m.moveTo(0) // All cards: everything
	if len(m.marketRows) != 2 || len(m.marketComps) != 2 {
		t.Errorf("all-cards market = %d rows, %d comps, want 2 and 2",
			len(m.marketRows), len(m.marketComps))
	}
}

// The four market tables hold fixed regions: each scrolls its own rows
// inside its budget, an overflowing section says where it is on its title
// line, and an empty one keeps its title over a note.
func TestMarketSectionRegionsScroll(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.view = viewMarket
	m.marketLoaded = true
	m.focus = paneCards
	for _, n := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		m.marketRows = append(m.marketRows, market.Row{Kind: market.KindProfit, Opportunity: opp(n, 1, 10)})
	}
	m.marketComps = []market.Comp{comp("C1", 60, 55, 44), comp("C2", 50, 45, 40), comp("C3", 40, 35, 30)}

	// Height 22 → 16 visible rows (the market help line wraps once at this
	// width); 11 lines of furniture leave a pool of 5. Both live sections
	// are overfull, so they share it: profit 3, comps 2.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 22})
	m = next.(Model)
	if got := m.marketSectionBudgets(); got != [4]int{3, 0, 0, 2} {
		t.Fatalf("budgets = %v, want [3 0 0 2]", got)
	}

	view := strings.Join(m.marketLines(110), "\n")
	if !strings.Contains(view, "1–3 of 6") || !strings.Contains(view, "1–2 of 3") {
		t.Fatalf("overflow positions missing:\n%s", view)
	}
	if strings.Count(view, "nothing today") != 2 {
		t.Errorf("empty sections should keep their titles over a note:\n%s", view)
	}
	if !strings.Contains(view, "P1") || strings.Contains(view, "P4") {
		t.Errorf("profit region should show rows 1–3 only:\n%s", view)
	}

	// Walking past the region's bottom scrolls only that section.
	for range 3 {
		m = key(m, "down")
	}
	if m.marketSecOffset != [4]int{1, 0, 0, 0} {
		t.Fatalf("offsets = %v, want the profit section scrolled once", m.marketSecOffset)
	}
	view = strings.Join(m.marketLines(110), "\n")
	if !strings.Contains(view, "2–4 of 6") || !strings.Contains(view, "C1") {
		t.Errorf("profit scrolled without disturbing comps:\n%s", view)
	}

	// Crossing into comps scrolls that region independently.
	for range 5 {
		m = key(m, "down")
	}
	if sec, idx := m.marketCursorPos(); sec != compsSection || idx != 2 {
		t.Fatalf("cursor at (%d,%d), want the last comp", sec, idx)
	}
	if m.marketSecOffset[compsSection] != 1 {
		t.Errorf("comps offset = %d, want 1", m.marketSecOffset[compsSection])
	}

	// An underfull section donates its slack: with one comp, comps takes 1
	// and the profit table gets the rest of the pool.
	m.marketComps = m.marketComps[:1]
	m.cursor[paneCards] = 0
	m.marketSecOffset = [4]int{}
	if got := m.marketSectionBudgets(); got != [4]int{4, 0, 0, 1} {
		t.Errorf("budgets = %v, want the slack donated to the overfull table", got)
	}
}

// ]/[ jump between market tables: straight to the next non-empty
// section's first row, skipping the empty ones, clamping at the ends.
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

	m = key(m, "down") // mid-table, so the jump has something to skip
	m = key(m, "]")    // over the two empty sections straight to comps
	if m.cursor[paneCards] != len(m.marketRows) {
		t.Fatalf("cursor = %d, want the comps section's first row", m.cursor[paneCards])
	}
	m = key(m, "]") // nothing beyond comps: the cursor stays
	if m.cursor[paneCards] != len(m.marketRows) {
		t.Errorf("cursor = %d, want to clamp at the last table", m.cursor[paneCards])
	}
	m = key(m, "[") // back to the profit table's first row
	if m.cursor[paneCards] != 0 {
		t.Fatalf("cursor = %d, want the profit table's first row", m.cursor[paneCards])
	}
	m = key(m, "[") // nothing before it: stays
	if m.cursor[paneCards] != 0 {
		t.Errorf("cursor = %d, want to clamp at the first table", m.cursor[paneCards])
	}

	// The jump is a card-pane gesture even when the hand is on the left.
	m.focus = paneContainers
	m = key(m, "]")
	if m.focus != paneCards || m.cursor[paneCards] != len(m.marketRows) {
		t.Errorf("jump from the container pane: focus %v, cursor %d", m.focus, m.cursor[paneCards])
	}
}

// Cycling the floor re-derives from the pristine rows without touching the
// store: what the floor hid comes back when it lifts.
func TestCycleFloorKeepsPristine(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", "nonfoil", 4, 30, 34),
		mover("Sol Ring-id", "nonfoil", 1, 1, 2), // under the $5 floor
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 2 {
		t.Fatalf("movers = %d, want 2 before the floor", len(m.movers))
	}

	st.err = errors.New("the floor must not re-query")
	m = key(m, "M") // $5
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Fatalf("floored movers = %+v", m.movers)
	}
	for range 3 {
		m = key(m, "M") // $10 → $25 → off
	}
	if len(m.movers) != 2 {
		t.Errorf("movers = %d after the floor lifted, want the hidden row back", len(m.movers))
	}
	if m.statusErr {
		t.Errorf("cycling the floor touched the store: %q", m.status)
	}
}

// Reload re-reads the analytical view too — it now depends on membership,
// so "reloaded" must include the rows on screen.
func TestReloadRederivesView(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", "nonfoil", 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 1 {
		t.Fatalf("movers = %d, want 1", len(m.movers))
	}
	st.movers = append(st.movers, mover("Solitude-id", "nonfoil", 1, 30, 34))
	m = key(m, "r")
	if len(m.movers) != 2 {
		t.Errorf("movers = %d after reload, want the new row", len(m.movers))
	}
	if m.cursor[paneContainers] != 0 {
		t.Errorf("reload moved the container cursor to %d", m.cursor[paneContainers])
	}
}
