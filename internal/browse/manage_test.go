package browse

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

func watchRow(name string, price *float64, op string, threshold float64, lastState string) store.WatchStatus {
	w := store.WatchStatus{Name: name, PriceUSD: price}
	w.ScryfallID = name + "-id"
	w.Display, w.Finish, w.Op, w.Threshold, w.LastState = name, finish.Nonfoil, op, threshold, lastState
	return w
}

func TestWatchesView(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchRow("Waiting", price(50), "under", 10, "unmet"),
		watchRow("Methas", price(5), "under", 10, "met"),
	}
	m := newTestModel(t, st)
	for range 4 {
		m = key(m, "v")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}

	if len(m.unders) != 2 || len(m.overs) != 0 {
		t.Fatalf("split = %d over, %d under, want both under", len(m.overs), len(m.unders))
	}
	if m.unders[0].Name != "Methas" {
		t.Errorf("default order = %s first, want the met watch leading", m.unders[0].Name)
	}
	out := m.View()

	for _, want := range []string{"WATCHES", "UNDERS", "THRESHOLD", "$10.00", "met", "waiting"} {
		if !strings.Contains(out, want) {
			t.Errorf("watches screen missing %q:\n%s", want, out)
		}
	}

	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "Methas") {
		t.Fatalf("d staged %+v, want the watch removal", m.confirm)
	}
	m = key(m, "y")
	if len(st.watches) != 1 {
		t.Fatalf("watches after removal = %d, want 1", len(st.watches))
	}
	m = key(m, "u")
	if len(st.watches) != 2 {
		t.Errorf("watches after undo = %d, want the watch re-added", len(st.watches))
	}
}

func TestFiredBannerOnOpen(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchRow("Ragavan", price(46), "under", 50, ""),
	}
	m := newTestModel(t, st)
	if !strings.Contains(m.status, "1 watch met its threshold: Ragavan under $50.00") {
		t.Errorf("banner = %q", m.status)
	}
	if st.watches[0].LastState != "" {
		t.Error("the banner consumed the alert state")
	}
	m = key(m, "tab")
	if m.status != "" {
		t.Errorf("status = %q after tab, want the banner dismissed", m.status)
	}
}

func TestWatchPromptFromCard(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")
	m = key(m, "w")
	if m.prompt == nil || !strings.Contains(m.prompt.label, "Sol Ring") {
		t.Fatalf("w opened %+v", m.prompt)
	}
	for _, r := range "40" {
		m = key(m, string(r))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(st.watches) != 1 {
		t.Fatalf("watches = %d after commit", len(st.watches))
	}
	w := st.watches[0]
	if w.Op != "over" || w.Threshold != 40 {
		t.Errorf("watch = %s $%.0f, want over 40 inferred from a price below it", w.Op, w.Threshold)
	}
	if !strings.Contains(m.status, "watching Sol Ring") {
		t.Errorf("status = %q", m.status)
	}
}

func TestParseThreshold(t *testing.T) {
	p := price(46.20)
	for _, tc := range []struct {
		text    string
		price   *float64
		op      string
		wantErr bool
	}{
		{"40", p, "under", false},
		{"50", p, "over", false},
		{"under 60", p, "under", false},
		{"over 10", p, "over", false},
		{"<40", p, "under", false},
		{">40", p, "over", false},
		{"$40", p, "under", false},
		{"40", nil, "", true},
		{"under 40", nil, "under", false},
		{"nonsense", p, "", true},
		{"-3", p, "", true},
	} {
		op, _, err := parseThreshold(tc.text, tc.price)
		if tc.wantErr != (err != nil) {
			t.Errorf("parseThreshold(%q) err = %v", tc.text, err)
			continue
		}
		if err == nil && op != tc.op {
			t.Errorf("parseThreshold(%q) op = %q, want %q", tc.text, op, tc.op)
		}
	}
}

func TestBinderManagement(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)

	m = key(m, "n")
	if m.prompt == nil {
		t.Fatal("n did not open the new-binder prompt")
	}
	for _, r := range "Trade" {
		m = key(m, string(r))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(st.binders) != 1 {
		t.Fatalf("binders = %v after create", st.binders)
	}
	if !strings.Contains(m.status, "created binder Trade") {
		t.Errorf("status = %q", m.status)
	}

	m.focus = paneContainers
	m.cursor[paneContainers] = 1
	m = key(m, "R")
	if m.prompt == nil || m.prompt.text != store.LooseName {
		t.Errorf("default rename: prompt=%+v, want prefilled with %q", m.prompt, store.LooseName)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if i := findContainer(m, "Trade"); i < 0 {
		t.Fatalf("created binder not in the pane: %+v", m.containers)
	} else {
		m.cursor[paneContainers] = i
	}
	m = key(m, "R")
	if m.prompt == nil || m.prompt.text != "Trade" {
		t.Fatalf("rename prompt = %+v, want prefilled", m.prompt)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	for _, r := range "Stock" {
		m = key(m, string(r))
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	found := false
	for _, name := range st.binders {
		if name == "Stock" {
			found = true
		}
	}
	if !found {
		t.Errorf("binders = %v after rename", st.binders)
	}

	if i := findContainer(m, "Stock"); i >= 0 {
		m.cursor[paneContainers] = i
	}
	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "Stock") {
		t.Fatalf("d staged %+v", m.confirm)
	}
	m = key(m, "y")
	if len(st.binders) != 0 {
		t.Errorf("binders = %v after removal", st.binders)
	}
}

func findContainer(m Model, name string) int {
	for i, c := range m.containers {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func TestMoversWindowCycle(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{{Name: "Riser", Finish: finish.Nonfoil, Copies: 1, Old: 1, New: 2}}
	m := newTestModel(t, st)
	m = key(m, "v")
	m = key(m, "W")
	if !strings.Contains(m.status, "last 90 days") {
		t.Errorf("status = %q after W", m.status)
	}
	m = key(m, "W")
	if !strings.Contains(m.status, "last 7 days") {
		t.Errorf("status = %q after second W", m.status)
	}
	m = key(m, "W")
	if !strings.Contains(m.status, "last 30 days") {
		t.Errorf("status = %q after third W, want the wrap back to the default", m.status)
	}

	m = key(m, "v")
	m = key(m, "v")
	m = key(m, "v")
	before := m.status
	m = key(m, "W")
	if m.status != before {
		t.Errorf("W outside movers changed status to %q", m.status)
	}
}

func TestPaletteRanksViewCommands(t *testing.T) {
	st := testStore()
	st.movers = nil
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }),
		WithBackfill(func(ctx context.Context, p progress.Fn, days int) (string, error) { return "", nil }),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m = key(m, "v")
	m = key(m, ":")

	top := []string{
		m.commands[m.palette.matches[0].index].id,
		m.commands[m.palette.matches[1].index].id,
	}
	for _, id := range top {
		if id != "op.update-prices" && id != "op.backfill" {
			t.Errorf("top palette entries on empty movers = %v, want the price ops leading", top)
		}
	}

	m.palette = nil
	if line := m.statusLine(); !strings.Contains(line, "F fetches prices") {
		t.Errorf("empty movers status = %q, want the populate guidance", line)
	}
}

func TestPopulateMoversComposes(t *testing.T) {
	st := testStore()
	var order []string
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "prices")
			return "prices updated", nil
		}),
		WithBackfill(func(ctx context.Context, p progress.Fn, days int) (string, error) {
			order = append(order, "backfill")
			return "backfilled 12", nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m = key(m, "v")
	cmd := (&m).populateView()
	if cmd == nil || m.op == nil {
		t.Fatal("F did not start the movers pipeline")
	}

	msg := findOpDone(t, cmd)
	if msg.outcome.summary != "prices updated · backfilled 12" {
		t.Errorf("summary = %q", msg.outcome.summary)
	}
	if strings.Join(order, ",") != "prices,backfill" {
		t.Errorf("pipeline order = %v", order)
	}
}

func findOpDone(t *testing.T, cmd tea.Cmd) opDoneMsg {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case opDoneMsg:
			return msg
		case tea.BatchMsg:
			for _, b := range msg {
				queue = append(queue, b)
			}
		}
	}
	t.Fatal("no opDoneMsg in the command tree")
	return opDoneMsg{}
}

func TestWatchByNameChainedPrompts(t *testing.T) {
	st := testStore()
	var gotName, gotOp string
	var gotThreshold float64
	m, err := New(st, WithWatchAddByName(func(ctx context.Context, p progress.Fn,
		name, op string, threshold float64) (string, error) {
		gotName, gotOp, gotThreshold = name, op, threshold
		return "watching " + name, nil
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)

	typeText := func(m Model, text string) Model {
		for _, r := range text {
			if r == ' ' {
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
				m = next.(Model)
				continue
			}
			m = key(m, string(r))
		}
		return m
	}

	(&m).promptWatchByName()
	m = typeText(m, "Sol Ring")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || !strings.Contains(m.prompt.label, "threshold") {
		t.Fatalf("second prompt = %+v", m.prompt)
	}

	m = typeText(m, "40")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || m.prompt.err == "" {
		t.Fatal("bare threshold accepted without a direction")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	m = typeText(m, "under 40")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.op == nil {
		t.Fatal("commit did not start the watch-add operation")
	}

	done := findOpDone(t, cmd)
	if gotName != "Sol Ring" || gotOp != "under" || gotThreshold != 40 {
		t.Errorf("op got %q %q %v, want Sol Ring under 40", gotName, gotOp, gotThreshold)
	}
	next, _ = m.Update(done)
	m = next.(Model)
	if !strings.Contains(m.status, "watching Sol Ring") {
		t.Errorf("status = %q", m.status)
	}
}

func TestWatchPickFlow(t *testing.T) {
	m := newTestModel(t, testStore())
	for range 4 {
		m = key(m, "v")
	}
	m, _ = runPaletteCommand(t, m, "watch.pick")
	if m.view != viewHoldings || !m.filtering || !m.watchPick {
		t.Fatalf("pick did not open holdings+filter: view=%v filtering=%v pick=%v",
			m.view, m.filtering, m.watchPick)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.watchPick {
		t.Fatal("enter must consume the pick")
	}
	if m.prompt == nil || !strings.Contains(m.prompt.label, "watch") {
		t.Fatalf("prompt = %+v, want the watch threshold prompt", m.prompt)
	}

	for _, r := range "under 5" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.statusErr {
		t.Fatalf("watch refused: %q", m.status)
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v after the watch landed, want watches", m.view)
	}
}

func TestWatchPickEscCancels(t *testing.T) {
	m := newTestModel(t, testStore())
	m, _ = runPaletteCommand(t, m, "watch.pick")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.watchPick {
		t.Fatal("esc must cancel the pick")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches after cancelling the pick", m.view)
	}
	if m.confirm != nil {
		t.Fatal("cancelling a pick must not fall through to the quit confirm")
	}
}

func TestWatchPickTabReachesContainers(t *testing.T) {
	m := newTestModel(t, testStore())
	m, _ = runPaletteCommand(t, m, "watch.pick")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.filtering || m.focus != paneContainers {
		t.Fatalf("filtering=%v focus=%v, want the bar closed and the containers pane", m.filtering, m.focus)
	}
	if !m.watchPick {
		t.Fatal("crossing panes must not abandon the pick")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.focus != paneCards || !m.watchPick {
		t.Fatalf("focus=%v watchPick=%v, want the cards pane with the pick armed", m.focus, m.watchPick)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("enter on a card must open the threshold prompt")
	}
}

func TestWatchPickPromptEscReturnsToWatches(t *testing.T) {
	m := newTestModel(t, testStore())
	m, _ = runPaletteCommand(t, m, "watch.pick")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("setup: no threshold prompt after the pick")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.prompt != nil {
		t.Fatal("esc must close the prompt")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches after cancelling the prompt", m.view)
	}
}

func TestWatchPickAbandonedByViewSwitch(t *testing.T) {
	m := newTestModel(t, testStore())
	m, _ = runPaletteCommand(t, m, "watch.pick")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = next
	m2 := newTestModel(t, testStore())
	m2, _ = runPaletteCommand(t, m2, "watch.pick")
	m2.filtering = false
	m2 = key(m2, "v")
	if m2.watchPick {
		t.Fatal("switching views must abandon the pick")
	}
}

func TestWatchEditPrefillsThreshold(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Ragavan"}
	w.ID, w.ScryfallID, w.Display = 1, "w1", "Ragavan"
	w.Finish, w.Op, w.Threshold = finish.Nonfoil, "under", 50
	st.watches = []store.WatchStatus{w}
	m := newTestModel(t, st)
	for range 4 {
		m = key(m, "v")
	}
	m = key(m, "w")
	if m.prompt == nil {
		t.Fatal("w did not open the threshold prompt")
	}
	if m.prompt.text != "under 50" {
		t.Fatalf("prefill = %q, want %q", m.prompt.text, "under 50")
	}
	if !strings.Contains(m.helpLine(), "under 40 / over 40") {
		t.Fatalf("help = %q, want the threshold syntax", m.helpLine())
	}
}

func TestPopulateUnpricedRunsTheUpdate(t *testing.T) {
	st := testStore()
	var order []string
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "prices")
			return "prices updated", nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	for m.view != viewWatches {
		m = key(m, "v")
	}
	cmd := (&m).populateView()
	if cmd == nil || m.op == nil {
		t.Fatal("F did not start the watches screen's pipeline")
	}
	msg := findOpDone(t, cmd)
	if msg.outcome.summary != "prices updated" {
		t.Errorf("summary = %q", msg.outcome.summary)
	}
	if strings.Join(order, ",") != "prices" {
		t.Errorf("pipeline order = %v", order)
	}
}

func TestWatchCommandAvailability(t *testing.T) {
	m := newTestModel(t, testStore())
	pick := func(id string) *command {
		t.Helper()
		for i := range m.commands {
			if m.commands[i].id == id {
				return &m.commands[i]
			}
		}
		t.Fatalf("no %s command in the registry", id)
		return nil
	}

	if pick("watch.pick").applies(&m) {
		t.Error("watch.pick offered on holdings, where w reaches cards directly")
	}
	for m.view != viewWatches {
		m = key(m, "v")
	}
	if !pick("watch.pick").applies(&m) {
		t.Error("watch.pick missing from the watches view")
	}

	for m.view != viewMarket {
		m = key(m, "v")
	}
	if pick("watch.pick").applies(&m) {
		t.Error("watch.pick offered on the market view")
	}
	m.marketRows = []market.Row{{Kind: market.KindProfit, Opportunity: market.Opportunity{
		Card:   store.OwnedFinish{ScryfallID: "sf-sol", Name: "Sol Ring", Finish: finish.Nonfoil},
		Market: 3.50, HasMarket: true,
	}}}
	m.cursor[paneCards] = 0
	if !pick("watch.add").applies(&m) {
		t.Error("watch.add missing from the market view")
	}
	m = key(m, "w")
	if m.prompt == nil || !strings.Contains(m.prompt.label, "Sol Ring") ||
		!strings.Contains(m.prompt.label, "$3.50") {
		t.Fatalf("w on a market row opened %+v, want the threshold prompt anchored on the market price", m.prompt)
	}
}

func TestFirstRunCatalogAutoRuns(t *testing.T) {
	ran := false
	m, err := New(testStore(),
		WithCatalogUpdate(func(ctx context.Context, p progress.Fn) (string, error) {
			ran = true
			return "catalog ready", nil
		}),
		WithCatalogOffer(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)
	m = pump(t, m, m.Init())
	if !ran {
		t.Fatal("opening with no catalog must start the catalog update")
	}

	ran = false
	quiet, err := New(testStore(),
		WithCatalogUpdate(func(ctx context.Context, p progress.Fn) (string, error) {
			ran = true
			return "", nil
		}),
		WithCatalogOffer(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pump(t, quiet, quiet.Init())
	if ran {
		t.Error("an existing catalog must not re-run the first-run download")
	}
}
