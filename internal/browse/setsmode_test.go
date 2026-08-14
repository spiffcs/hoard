package browse

import (
	"github.com/spiffcs/hoard/internal/finish"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

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

func atSet(t *testing.T, m Model, code string) Model {
	t.Helper()
	m.cursor[paneContainers] = setRowAt(t, m, code)
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.deriveView()
	return m
}

func TestLaunchesInSetsMode(t *testing.T) {
	m, err := New(testStore(), WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !m.setsMode {
		t.Error("the browser must open on the sets lens")
	}
	if m.containers[0].Kind != kindAllCards {
		t.Fatalf("first row = %+v, want All Cards", m.containers[0])
	}
	for i, want := range []string{"c21", "mh3", "uma"} {
		if c := m.containers[i+1]; c.Kind != kindSet || c.setCode != want {
			t.Errorf("row %d = %+v, want set %s", i+1, c, want)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if v := next.(Model).View(); !strings.Contains(v, "SETS") {
		t.Error("the first frame's pane title must say SETS")
	}

	m = key(m, "B")
	if m.setsMode {
		t.Error("B did not leave sets mode")
	}
	if m.containers[1].Kind != store.KindCollection {
		t.Errorf("row 1 = %+v, want the binder", m.containers[1])
	}
}

func TestSetsToggleRoundTrip(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	if !m.setsMode {
		t.Fatal("B did not enter sets mode")
	}
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want the snap to All Cards", m.cursor[paneContainers])
	}
	if m.containers[0].Kind != kindAllCards {
		t.Fatalf("first row = %+v, want All Cards", m.containers[0])
	}

	if len(m.containers) != 4 {
		t.Fatalf("containers = %+v, want All Cards + c21/mh3/uma", m.containers)
	}
	for i, want := range []string{"c21", "mh3", "uma"} {
		if c := m.containers[i+1]; c.Kind != kindSet || c.setCode != want {
			t.Errorf("row %d = %+v, want set %s", i+1, c, want)
		}
	}
	if !strings.Contains(m.View(), "SETS") {
		t.Error("the pane title must say SETS")
	}

	if m.containers[0].Value == 0 {
		t.Error("All Cards row lost its rollup")
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
		t.Errorf("All Cards rollup = %+v, want the sets summed", m.containers[0])
	}
}

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

func TestMoversScopedToSet(t *testing.T) {
	st := testStore()
	solitude := mover("Solitude-id", finish.Nonfoil, 1, 30, 34)
	solitude.SetCode = "mh3"
	st.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
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

func TestUnpricedScopedToSetWithEligibility(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{
		{ScryfallID: "Bitterblossom-id", Name: "Bitterblossom", SetCode: "uma",
			CollectorNumber: "85", Finish: finish.Nonfoil, Copies: 1, HeldIn: "Binder"},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	for m.view != viewWatches {
		m = key(m, "v")
	}
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
	m = key(m, "tab")
	m = key(m, "down")
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
	w1.ScryfallID, w1.SetCode, w1.Finish, w1.Op, w1.Threshold = "Bitterblossom-id", "uma", finish.Nonfoil, "under", 30
	w2 := store.WatchStatus{Name: "Solitude", PriceUSD: price(34)}
	w2.ScryfallID, w2.SetCode, w2.Finish, w2.Op, w2.Threshold = "Solitude-id", "mh3", finish.Nonfoil, "over", 50
	st.watches = []store.WatchStatus{w1, w2}
	m := newTestModel(t, st)
	m = key(m, "B")
	m.showView(viewWatches)
	if len(shownWatches(m)) != 2 {
		t.Fatalf("all-cards watches = %d, want both", len(shownWatches(m)))
	}
	m = atSet(t, m, "mh3")

	if len(m.overs) != 1 || len(m.unders) != 0 || m.overs[0].Name != "Solitude" {
		t.Errorf("mh3 watches = %+v/%+v, want Solitude alone", m.overs, m.unders)
	}
}

func TestShowViewSnapsIneligibleSet(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Bitterblossom", PriceUSD: price(34)}
	w.ScryfallID, w.SetCode, w.Finish, w.Op, w.Threshold = "Bitterblossom-id", "uma", finish.Nonfoil, "<=", 30
	st.watches = []store.WatchStatus{w}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSet(t, m, "c21")
	m.showView(viewWatches)
	if m.cursor[paneContainers] != 0 {
		t.Errorf("cursor = %d, want the snap to All Cards", m.cursor[paneContainers])
	}
	if !strings.Contains(m.status, "has no watches") {
		t.Errorf("status = %q, want the snap explained", m.status)
	}
}

func TestMarketScopedToSet(t *testing.T) {
	umaOpp := opp("Bitter", 2, 20)
	umaOpp.Card.SetCode = "uma"
	umaComp := comp("BitterC", 60, 55, 44)
	umaComp.Card.SetCode = "uma"
	res := market.Result{
		Opportunities: []market.Opportunity{umaOpp, opp("Sol", 2, 20)},
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

func TestSetContainerRowRefusesEdits(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = atSet(t, m, "uma")

	m = key(m, "d")
	if !m.statusErr || !strings.Contains(m.status, "not where they live") {
		t.Errorf("d on the set row: status = %q err=%v", m.status, m.statusErr)
	}
	m.status, m.statusErr = "", false
	m = key(m, "R")
	if !m.statusErr || !strings.Contains(m.status, "named by Wizards") {
		t.Errorf("R on the set row: status = %q err=%v", m.status, m.statusErr)
	}
}

func heldIn(id int64, name string, qty int) store.Holding {
	return store.Holding{
		ContainerID: id, ContainerName: name, ContainerKind: store.KindCollection,
		Finish: finish.Nonfoil, Board: "main", Quantity: qty,
	}
}

func deckCopy(qty int) store.EntryView {
	e := entry("Bitterblossom", "main", finish.Nonfoil, qty, 34)
	e.Card.SetCode, e.Card.CollectorNumber = "uma", "85"
	return e
}

func atSetCard(t *testing.T, m Model, code, name string) Model {
	t.Helper()
	m = atSet(t, m, code)
	m = key(m, "tab")
	for i, c := range m.cards {
		if c.Name == name {
			m.cursor[paneCards] = i
			return m
		}
	}
	t.Fatalf("no card %s in %s: %+v", name, code, m.cards)
	return m
}

func qtyIn(st *fakeStore, cid int64, id string, fin finish.Finish) int {
	for _, r := range st.rowsIn(cid) {
		if r.ScryfallID == id && r.Finish == fin {
			return r.Quantity
		}
	}
	return 0
}

func TestSetRowAdjustsTheOneBinder(t *testing.T) {
	st := testStore()
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {heldIn(1, "Binder", 4)},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "+")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 5 {
		t.Errorf("binder quantity = %d after +, want 5", got)
	}
	if m.statusErr || !strings.Contains(m.status, "×5 in Binder") {
		t.Errorf("status = %q err=%v, want the receipt naming the binder", m.status, m.statusErr)
	}
	m = key(m, "u")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 4 {
		t.Errorf("binder quantity = %d after undo, want 4 back", got)
	}
}

func TestSetRowAdjustUsesTheBinderCountNotTheRowTotal(t *testing.T) {
	st := testStore()

	st.deckCards[202] = append(st.deckCards[202], deckCopy(1))
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {
			heldIn(1, "Binder", 4),
			{ContainerID: 202, ContainerName: "Rich Deck", ContainerKind: store.KindDeck,
				Finish: finish.Nonfoil, Board: "main", Quantity: 1},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")
	if c := m.selectedCard(); c == nil || c.Quantity != 5 {
		t.Fatalf("set row = %+v, want the 4 loose + 1 in deck total", c)
	}

	m = key(m, "+")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 5 {
		t.Errorf("binder quantity = %d after +, want 5 (not 6, the row total + 1)", got)
	}
}

func TestSetRowAdjustRefusesAcrossBinders(t *testing.T) {
	st := testStore()
	st.binders = map[int64]string{7: "Trades"}
	st.binderRows = map[int64][]store.CollectionRow{
		7: {row("Bitterblossom", "uma", "85", finish.Nonfoil, 2, 68)},
	}
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {heldIn(1, "Binder", 4), heldIn(7, "Trades", 2)},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "2 binders") || !strings.Contains(m.status, "enter") {
		t.Errorf("status = %q err=%v, want the count and the pointer at the detail", m.status, m.statusErr)
	}
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 4 {
		t.Errorf("binder quantity = %d, want it untouched", got)
	}
	if got := qtyIn(st, 7, "Bitterblossom-id", finish.Nonfoil); got != 2 {
		t.Errorf("Trades quantity = %d, want it untouched", got)
	}
}

func TestSetRowWithOnlyDeckCopiesRefuses(t *testing.T) {
	st := testStore()
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {
			{ContainerID: 202, ContainerName: "Rich Deck", ContainerKind: store.KindDeck,
				Finish: finish.Nonfoil, Board: "main", Quantity: 1},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "imported list") {
		t.Errorf("+ status = %q err=%v, want the deck refusal", m.status, m.statusErr)
	}
	m.status, m.statusErr = "", false
	m = key(m, "d")
	if m.confirm != nil {
		t.Fatalf("d staged %+v, want no question for deck-only copies", m.confirm)
	}
	if !m.statusErr || !strings.Contains(m.status, "imported list") {
		t.Errorf("d status = %q err=%v, want the deck refusal", m.status, m.statusErr)
	}
}

func TestSetRowRemoveFromOneBinder(t *testing.T) {
	st := testStore()
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {heldIn(1, "Binder", 4)},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "d")
	if m.confirm == nil {
		t.Fatal("d staged no confirm")
	}
	if !strings.Contains(m.confirm.prompt, "×4 from Binder") {
		t.Errorf("prompt = %q, want the binder and count named", m.confirm.prompt)
	}
	m = key(m, "y")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 0 {
		t.Errorf("binder quantity = %d after removal, want gone", got)
	}
	if m.statusErr || !strings.Contains(m.status, "removed Bitterblossom (nonfoil) from Binder") {
		t.Errorf("status = %q err=%v, want the removal receipt", m.status, m.statusErr)
	}
	m = key(m, "u")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 4 {
		t.Errorf("binder quantity = %d after undo, want 4 back", got)
	}
}

func TestSetRowRemoveAcrossBindersUndoesTogether(t *testing.T) {
	st := testStore()
	st.binders = map[int64]string{7: "Trades"}
	st.binderRows = map[int64][]store.CollectionRow{
		7: {row("Bitterblossom", "uma", "85", finish.Nonfoil, 2, 68)},
	}
	st.deckCards[202] = append(st.deckCards[202], deckCopy(1))
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {
			heldIn(1, "Binder", 4), heldIn(7, "Trades", 2),
			{ContainerID: 202, ContainerName: "Rich Deck", ContainerKind: store.KindDeck,
				Finish: finish.Nonfoil, Board: "main", Quantity: 1},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "from 2 binders?") {
		t.Fatalf("confirm = %+v, want the binder count named", m.confirm)
	}
	m = key(m, "y")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 0 {
		t.Errorf("Binder quantity = %d, want emptied", got)
	}
	if got := qtyIn(st, 7, "Bitterblossom-id", finish.Nonfoil); got != 0 {
		t.Errorf("Trades quantity = %d, want emptied", got)
	}
	if n := len(st.deckCards[202]); n != 3 {
		t.Errorf("deck entries = %d, want the deck's copy untouched", n)
	}
	if !strings.Contains(m.status, "from 2 binders") {
		t.Errorf("status = %q, want the multi-binder receipt", m.status)
	}

	m = key(m, "u")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 4 {
		t.Errorf("Binder quantity = %d after undo, want 4 back", got)
	}
	if got := qtyIn(st, 7, "Bitterblossom-id", finish.Nonfoil); got != 2 {
		t.Errorf("Trades quantity = %d after undo, want 2 back", got)
	}
}

func TestSetRowRemoveKeepsTheOtherFinish(t *testing.T) {
	st := testStore()
	foil := row("Bitterblossom", "uma", "85", finish.Foil, 1, 60)
	st.collection = append(st.collection, foil)
	st.holdingsByID = map[string][]store.Holding{
		"Bitterblossom-id": {
			heldIn(1, "Binder", 4),
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Foil, Board: "main", Quantity: 1},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "B")
	m = atSetCard(t, m, "uma", "Bitterblossom")

	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "(nonfoil)") {
		t.Fatalf("confirm = %+v, want the nonfoil row named", m.confirm)
	}
	m = key(m, "y")
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Nonfoil); got != 0 {
		t.Errorf("nonfoil quantity = %d, want gone", got)
	}
	if got := qtyIn(st, 1, "Bitterblossom-id", finish.Foil); got != 1 {
		t.Errorf("foil quantity = %d, want the unnamed finish left alone", got)
	}
}

func TestSetsModeCardHelpAdvertisesEdits(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = atSet(t, m, "uma")
	m = key(m, "tab")
	if h := m.helpLine(); !strings.Contains(h, "+/- qty · d remove · u undo") {
		t.Errorf("help = %q, want the edit keys on a set's cards", h)
	}
}

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

func TestNewBinderExitsSetsMode(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "B")
	m = key(m, "tab")
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
