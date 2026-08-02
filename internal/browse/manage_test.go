package browse

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

func watchRow(name string, price *float64, op string, threshold float64, lastState string) store.WatchStatus {
	w := store.WatchStatus{Name: name, PriceUSD: price}
	w.ScryfallID = name + "-id"
	w.Display, w.Finish, w.Op, w.Threshold, w.LastState = name, "nonfoil", op, threshold, lastState
	return w
}

// The watches view joins the v cycle: rows render, met leads by default,
// and 'd' removes the selected watch with undo.
func TestWatchesView(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchRow("Waiting", price(50), "under", 10, "unmet"),
		watchRow("Methas", price(5), "under", 10, "met"),
	}
	m := newTestModel(t, st)
	for range 3 {
		m = key(m, "v")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	if m.watches[0].Name != "Methas" {
		t.Errorf("default order = %s first, want the met watch leading", m.watches[0].Name)
	}
	out := m.View()
	for _, want := range []string{"WATCHES", "under $10.00", "met", "waiting"} {
		if !strings.Contains(out, want) {
			t.Errorf("watches view missing %q:\n%s", want, out)
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

// The fired banner previews unacknowledged crossings read-only, and any
// status-clearing key dismisses it.
func TestFiredBannerOnOpen(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchRow("Ragavan", price(46), "under", 50, ""),
	}
	m := newTestModel(t, st)
	if !strings.Contains(m.status, "1 watch met its threshold — Ragavan under $50.00") {
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

// 'w' opens the threshold prompt on the selected card; a bare number infers
// the direction from the current price.
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
		{"40", nil, "", true}, // no price: nothing to infer from
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

// Binder management: n creates (undo deletes), R renames non-default
// binders only, d removes an empty one.
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

	// The default binder refuses a rename.
	m.focus = paneContainers
	m.cursor[paneContainers] = 0
	m = key(m, "R")
	if m.prompt != nil || !strings.Contains(m.status, "cannot be renamed") {
		t.Errorf("default rename: prompt=%v status=%q", m.prompt, m.status)
	}

	// Renaming the created binder works and undo renames back.
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

	// d on the empty binder removes it after confirm.
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

// findContainer locates a container row by name, -1 when absent.
func findContainer(m Model, name string) int {
	for i, c := range m.containers {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// W cycles the movers window and re-queries.
func TestMoversWindowCycle(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{{Name: "Riser", Finish: "nonfoil", Copies: 1, Old: 1, New: 2}}
	m := newTestModel(t, st)
	m = key(m, "v") // movers
	m = key(m, "W")
	if !strings.Contains(m.status, "last 7 days") {
		t.Errorf("status = %q after W", m.status)
	}
	m = key(m, "W")
	if !strings.Contains(m.status, "last 90 days") {
		t.Errorf("status = %q after second W", m.status)
	}
	// Outside movers, W is not bound.
	m = key(m, "v") // watches... continue to holdings
	m = key(m, "v")
	m = key(m, "v")
	before := m.status
	m = key(m, "W")
	if m.status != before {
		t.Errorf("W outside movers changed status to %q", m.status)
	}
}

// The empty-query palette leads with the commands that help the current
// view: on an empty movers view, updating and backfilling prices outrank
// the generic verbs.
func TestPaletteRanksViewCommands(t *testing.T) {
	st := testStore()
	st.movers = nil
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }),
		WithBackfill(func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m = key(m, "v") // movers, empty
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
	// The empty movers view says how to fill itself.
	m.palette = nil
	if line := m.statusLine(); !strings.Contains(line, "F fetches prices") {
		t.Errorf("empty movers status = %q, want the populate guidance", line)
	}
}

// F composes the movers pipeline: update prices, then backfill, as one
// operation with a joined summary.
func TestPopulateMoversComposes(t *testing.T) {
	st := testStore()
	var order []string
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "prices")
			return "prices updated", nil
		}),
		WithBackfill(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "backfill")
			return "backfilled 12", nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m = key(m, "v") // movers
	cmd := (&m).populateView()
	if cmd == nil || m.op == nil {
		t.Fatal("F did not start the movers pipeline")
	}
	// Run the composed op synchronously through the returned command batch:
	// execute the op function directly via the done message it will send.
	msg := findOpDone(t, cmd)
	if msg.outcome.summary != "prices updated · backfilled 12" {
		t.Errorf("summary = %q", msg.outcome.summary)
	}
	if strings.Join(order, ",") != "prices,backfill" {
		t.Errorf("pipeline order = %v", order)
	}
}

// findOpDone digs the opDoneMsg out of a startOp command batch by running
// its constituent Cmds; the runner is the one that yields it.
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

// The palette's watch-by-name flow chains two prompts and runs the add as
// an operation.
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

	// A bare number is refused — no price to infer direction from.
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

	// Run the op through its own command tree and feed the outcome back.
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

// The watch picker: from the watches view, "Add a watch" jumps to holdings
// with the filter open; enter on the narrowed list runs the watch prompt
// for that card; esc abandons the pick.
func TestWatchPickFlow(t *testing.T) {
	m := newTestModel(t, testStore())
	for range 3 {
		m = key(m, "v") // holdings → movers → unpriced → watches
	}
	m, _ = runPaletteCommand(t, m, "watch.pick")
	if m.view != viewHoldings || !m.filtering || !m.watchPick {
		t.Fatalf("pick did not open holdings+filter: view=%v filtering=%v pick=%v",
			m.view, m.filtering, m.watchPick)
	}
	// Enter straight from the filter bar picks the card under the cursor.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.watchPick {
		t.Fatal("enter must consume the pick")
	}
	if m.prompt == nil || !strings.Contains(m.prompt.label, "watch") {
		t.Fatalf("prompt = %+v, want the watch threshold prompt", m.prompt)
	}

	// Committing the threshold jumps back to the watches view, where the
	// new entry is visible.
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
	// First esc clears the filter bar, second cancels the pick.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.watchPick {
		t.Fatal("esc must cancel the pick")
	}
	if m.confirm != nil {
		t.Fatal("cancelling a pick must not fall through to the quit confirm")
	}
}

func TestWatchPickAbandonedByViewSwitch(t *testing.T) {
	m := newTestModel(t, testStore())
	m, _ = runPaletteCommand(t, m, "watch.pick")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // close the bar? no: picks…
	_ = next
	m2 := newTestModel(t, testStore())
	m2, _ = runPaletteCommand(t, m2, "watch.pick")
	m2.filtering = false // bar closed, pick still armed
	m2 = key(m2, "v")    // leave holdings
	if m2.watchPick {
		t.Fatal("switching views must abandon the pick")
	}
}

// Editing an existing watch prefills its threshold, and the prompt's help
// spells out the under/over syntax.
func TestWatchEditPrefillsThreshold(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Ragavan"}
	w.ID, w.ScryfallID, w.Display = 1, "w1", "Ragavan"
	w.Finish, w.Op, w.Threshold = "nonfoil", "under", 50
	st.watches = []store.WatchStatus{w}
	m := newTestModel(t, st)
	for range 3 {
		m = key(m, "v") // to watches
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

// F on unpriced runs the price refresh first, then the finish repair — the
// user pressing F there wants prices, not just a finish audit.
func TestPopulateUnpricedComposes(t *testing.T) {
	st := testStore()
	var order []string
	m, err := New(st,
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "prices")
			return "prices updated", nil
		}),
		WithRepairFinishes(func(ctx context.Context, p progress.Fn) (string, error) {
			order = append(order, "repair")
			return "every finish already correct", nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m = key(m, "v")
	m = key(m, "v") // unpriced
	cmd := (&m).populateView()
	if cmd == nil || m.op == nil {
		t.Fatal("F did not start the unpriced pipeline")
	}
	msg := findOpDone(t, cmd)
	if msg.outcome.summary != "prices updated · every finish already correct" {
		t.Errorf("summary = %q", msg.outcome.summary)
	}
	if strings.Join(order, ",") != "prices,repair" {
		t.Errorf("pipeline order = %v", order)
	}
}
