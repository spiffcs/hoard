package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
