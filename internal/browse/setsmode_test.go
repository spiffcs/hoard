package browse

// Sets mode: the left pane's by-set lens. Toggle round-trip, ordering, the
// per-set holdings read, the full view mask, eligibility, and the guards
// that keep the read-only lens from writing anywhere.

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

// setRowAt finds the sets-pane row for a code, failing if absent.
func setRowAt(t *testing.T, m Model, code string) int {
	t.Helper()
	for i, c := range m.containers {
		if c.Kind == kindSet && c.setCode == code {
			return i
		}
	}
	t.Fatalf("no set row for %s in %+v", code, m.containers)
	return -1
}

// atSet selects one set row and re-derives, the sets-mode atAllCards.
func atSet(t *testing.T, m Model, code string) Model {
	t.Helper()
	m.cursor[paneContainers] = setRowAt(t, m, code)
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.deriveView()
	return m
}

// B flips the pane to sets — All cards first, then the store's rows in its
// order, cursor snapped to All cards — and back to binders and decks.
func TestSetsToggleRoundTrip(t *testing.T) {
	m := newTestModel(t, testStore()) // cursor starts on the binder
	m = key(m, "B")
	if !m.setsMode {
		t.Fatal("B did not enter sets mode")
	}
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want the snap to All cards", m.cursor[paneContainers])
	}
	if m.containers[0].Kind != kindAllCards {
		t.Fatalf("first row = %+v, want All cards", m.containers[0])
	}
	// The fake derives one row per held set code, sorted by code.
	if len(m.containers) != 4 {
		t.Fatalf("containers = %+v, want All cards + c21/mh3/uma", m.containers)
	}
	for i, want := range []string{"c21", "mh3", "uma"} {
		if c := m.containers[i+1]; c.Kind != kindSet || c.setCode != want {
			t.Errorf("row %d = %+v, want set %s", i+1, c, want)
		}
	}
	if !strings.Contains(m.View(), "SETS") {
		t.Error("the pane title must say SETS")
	}
	// The All cards rollup covers the whole hoard, either lens.
	if m.containers[0].Value == 0 {
		t.Error("All cards row lost its rollup")
	}

	m = key(m, "B")
	if m.setsMode {
		t.Fatal("second B did not leave sets mode")
	}
	if m.containers[1].Kind != store.KindCollection {
		t.Errorf("row 1 = %+v, want the binder back", m.containers[1])
	}
	if !strings.Contains(m.View(), "COLLECTION") {
		t.Error("the pane title must say COLLECTION again")
	}
}

// The pane preserves the store's order verbatim — release-date ranking is
// the query's job, and re-sorting here would silently disagree with it.
func TestSetsPaneKeepsStoreOrder(t *testing.T) {
	st := testStore()
	st.sets = []store.SetSummary{
		{Code: "mh3", Name: "Modern Horizons 3", ReleasedAt: "2024-06-14", Copies: 3, Value: 125},
		{Code: "uma", Name: "Ultimate Masters", ReleasedAt: "2018-12-07", Copies: 5, Value: 270},
		{Code: "zzz", Name: "ZZZ", Copies: 1, Value: 1},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	for i, want := range []string{"Modern Horizons 3", "Ultimate Masters", "ZZZ"} {
		if m.containers[i+1].Name != want {
			t.Errorf("row %d = %q, want %q", i+1, m.containers[i+1].Name, want)
		}
	}
	if m.containers[0].Copies != 9 || m.containers[0].Value != 396 {
		t.Errorf("All cards rollup = %+v, want the sets summed", m.containers[0])
	}
}

// Selecting a set shows only that set's holdings, distinct printings kept
// distinct — no all-cards name merge.
func TestSetSelectionShowsSetHoldings(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = atSet(t, m, "uma")
	if len(m.cards) != 2 {
		t.Fatalf("uma cards = %+v, want its two printings", m.cards)
	}
	for _, c := range m.cards {
		if c.SetCode != "uma" {
			t.Errorf("card %s from %s leaked into uma", c.Name, c.SetCode)
		}
	}
}

// A selected set narrows movers, exactly like a container: the mask runs on
// set code, and All cards still shows everything.
func TestMoversScopedToSet(t *testing.T) {
	st := testStore()
	solitude := mover("Solitude-id", "nonfoil", 1, 30, 34)
	solitude.SetCode = "mh3"
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", "nonfoil", 4, 30, 34), // uma
		solitude,
	}
	m := newTestModel(t, testStore())
	m.store = st
	m = key(m, "B")
	m = key(m, "v")
	if len(m.movers) != 2 {
		t.Fatalf("all-cards movers = %d, want both", len(m.movers))
	}
	m = atSet(t, m, "uma")
	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Errorf("uma movers = %+v, want Bitterblossom alone", m.movers)
	}
	if title, _ := m.viewHeader(); !strings.Contains(title, "· UMA") {
		t.Errorf("header = %q, want the set named", title)
	}
}

// Unpriced and watches follow the same mask, and grey out the sets with
// nothing to show; the cursor skips the greyed rows.
func TestUnpricedScopedToSetWithEligibility(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{
		{ScryfallID: "Bitterblossom-id", Name: "Bitterblossom", SetCode: "uma",
			CollectorNumber: "85", Finish: "nonfoil", Copies: 1, HeldIn: "Binder"},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = key(m, "v") // movers
	m = key(m, "v") // unpriced
	if len(m.unpriced) != 1 {
		t.Fatalf("all-cards unpriced = %d, want 1", len(m.unpriced))
	}
	uma := setRowAt(t, m, "uma")
	for i, c := range m.containers {
		if c.Kind != kindSet {
			continue
		}
		if got := m.containerEligible(i); got != (i == uma) {
			t.Errorf("eligibility of %s = %v, want only uma eligible", c.setCode, got)
		}
	}
	m = key(m, "tab")  // container pane, at All cards
	m = key(m, "down") // skips c21 and mh3, lands on uma
	if m.cursor[paneContainers] != uma {
		t.Errorf("cursor = %d, want the greyed sets skipped to %d", m.cursor[paneContainers], uma)
	}
	if len(m.unpriced) != 1 || m.unpriced[0].SetCode != "uma" {
		t.Errorf("uma unpriced = %+v", m.unpriced)
	}
}

func TestWatchesScopedToSet(t *testing.T) {
	st := testStore()
	w1 := store.WatchStatus{Name: "Bitterblossom", PriceUSD: price(34)}
	w1.ScryfallID, w1.SetCode, w1.Finish, w1.Op, w1.Threshold = "Bitterblossom-id", "uma", "nonfoil", "<=", 30
	w2 := store.WatchStatus{Name: "Solitude", PriceUSD: price(34)}
	w2.ScryfallID, w2.SetCode, w2.Finish, w2.Op, w2.Threshold = "Solitude-id", "mh3", "nonfoil", ">=", 50
	st.watches = []store.WatchStatus{w1, w2}
	m := newTestModel(t, st)
	m = key(m, "B")
	m.showView(viewWatches)
	if len(m.watches) != 2 {
		t.Fatalf("all-cards watches = %d, want both", len(m.watches))
	}
	m = atSet(t, m, "mh3")
	if len(m.watches) != 1 || m.watches[0].Name != "Solitude" {
		t.Errorf("mh3 watches = %+v, want Solitude alone", m.watches)
	}
}

// Arriving on a view where the selected set has nothing snaps to All cards
// and says so, like an ineligible container.
func TestShowViewSnapsIneligibleSet(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Bitterblossom", PriceUSD: price(34)}
	w.ScryfallID, w.SetCode, w.Finish, w.Op, w.Threshold = "Bitterblossom-id", "uma", "nonfoil", "<=", 30
	st.watches = []store.WatchStatus{w}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSet(t, m, "c21") // holds no watch
	m.showView(viewWatches)
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want the snap to All cards", m.cursor[paneContainers])
	}
	if !strings.Contains(m.status, "has no watches") {
		t.Errorf("status = %q, want the snap explained", m.status)
	}
}

// The market narrows to the set before ranking, comps included.
func TestMarketScopedToSet(t *testing.T) {
	umaOpp := opp("Bitter", 2, 20)
	umaOpp.Card.SetCode = "uma"
	umaComp := comp("BitterC", 60, 55, 44)
	umaComp.Card.SetCode = "uma"
	res := market.Result{
		Opportunities: []market.Opportunity{umaOpp, opp("Sol", 2, 20)}, // Sol stays mh3
		Comps:         []market.Comp{umaComp, comp("SolC", 50, 45, 40)},
		Compared:      4,
	}
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m.view = viewMarket
	m.marketResult = res
	m.marketLoaded = true
	m = atSet(t, m, "uma")
	if len(m.marketRows) != 1 || m.marketRows[0].Card.Name != "Bitter" {
		t.Fatalf("uma market rows = %+v, want Bitter alone", m.marketRows)
	}
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Name != "BitterC" {
		t.Fatalf("uma comps = %+v, want BitterC alone", m.marketComps)
	}
}

// A set row is a read-only lens: quantity edits, removals, and renames all
// refuse, on both panes.
func TestSetRowRefusesEdits(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = atSet(t, m, "uma") // focus starts on the container pane

	m = key(m, "d")
	if !m.statusErr || !strings.Contains(m.status, "not where they live") {
		t.Errorf("d on the set row: status = %q err=%v", m.status, m.statusErr)
	}
	m.status, m.statusErr = "", false
	m = key(m, "R")
	if !m.statusErr || !strings.Contains(m.status, "named by Wizards") {
		t.Errorf("R on the set row: status = %q err=%v", m.status, m.statusErr)
	}

	m = key(m, "tab") // into the card pane
	m.status, m.statusErr = "", false
	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "every printing from") {
		t.Errorf("+ on a set's card: status = %q err=%v, want the refusal", m.status, m.statusErr)
	}
	m.status, m.statusErr = "", false
	m = key(m, "d")
	if !m.statusErr {
		t.Errorf("d on a set's card: status = %q, want a refusal", m.status)
	}
}

// Export refuses a set row rather than writing a binder ref that matches
// nothing.
func TestExportRefusesSetRow(t *testing.T) {
	m := newTestModel(t, testStore())
	called := false
	m.exportFn = func(binderRef, deckRef, format, path string) (string, error) {
		called = true
		return "", nil
	}
	m = key(m, "B")
	m = atSet(t, m, "uma")
	for i := range m.commands {
		if m.commands[i].id == "export.container" {
			m.commands[i].run(&m)
		}
	}
	if !m.statusErr || !strings.Contains(m.status, "can't be exported") {
		t.Errorf("export on a set: status = %q err=%v", m.status, m.statusErr)
	}
	if m.prompt != nil || called {
		t.Error("export must not proceed from a set row")
	}
}

// Creating a binder promises "switch to it", so n leaves sets mode and
// lands on the new binder.
func TestNewBinderExitsSetsMode(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = key(m, "tab") // the pane with n bound is either; use containers
	m = key(m, "n")
	if m.prompt == nil {
		t.Fatal("n did not open the binder prompt")
	}
	m.prompt.commit(&m, "Trades")
	if m.setsMode {
		t.Error("creating a binder must leave sets mode")
	}
	if sel := m.selectedContainer(); sel == nil || sel.Name != "Trades" {
		t.Errorf("selection = %+v, want the new binder focused", sel)
	}
}

// r reloads in place: sets mode survives, with the selection intact.
func TestReloadKeepsSetsMode(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = atSet(t, m, "uma")
	m = key(m, "r")
	if !m.setsMode {
		t.Fatal("reload dropped sets mode")
	}
	if sel := m.selectedContainer(); sel == nil || sel.Kind != kindSet || sel.setCode != "uma" {
		t.Errorf("selection after reload = %+v, want the uma row", sel)
	}
}
