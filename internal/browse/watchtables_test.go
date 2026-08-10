package browse

// The watches screen as three tables: OVERS, UNDERS and the UNPRICED
// holdings that used to be a view of their own. The negative controls are
// the point of this file — a table that renders somebody else's rows is
// the failure this change is most likely to introduce, so every assertion
// here is about which section a row landed in, not merely that it rendered
// somewhere.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

// watchOn builds a watch row in one direction. Op is the raw column the
// store writes; "under" and "over" are the only two validateWatch admits.
func watchOn(name, id, set, op string, threshold float64, price *float64) store.WatchStatus {
	w := store.WatchStatus{Name: name, SetCode: set, CollectorNumber: "1", PriceUSD: price}
	w.ScryfallID, w.Finish, w.Op, w.Threshold = id, "nonfoil", op, threshold
	return w
}

// shownWatches is every watch on screen across the two watch tables — the
// count the old single-table m.watches used to carry, for the tests that
// only care that a row survived the container or set filter. Which table it
// landed in is the negative controls' business.
func shownWatches(m Model) []store.WatchStatus {
	return append(append([]store.WatchStatus(nil), m.overs...), m.unders...)
}

// threeTableStore seeds one row for each of the three tables, with names,
// sets and ids pairwise distinct so a leak between sections is visible.
func threeTableStore() *fakeStore {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchOn("Overseer", "over-id", "aaa", "over", 5, price(9)),
		watchOn("Underling", "under-id", "bbb", "under", 50, price(9)),
	}
	st.unpriced = []store.UnpricedRow{{
		ScryfallID: "unpriced-id", Name: "Nopricehere", SetCode: "ccc",
		CollectorNumber: "3", Finish: "foil", Copies: 2, HeldIn: "Collection",
	}}
	return st
}

// onWatches puts the model on the watches screen, at All Cards, in a window
// tall enough for three tables to show a row each.
func onWatches(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m := atAllCards(t, newTestModel(t, st))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	(&m).showView(viewWatches)
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	return m
}

// THE SHAPE: one screen, three headings, in the owner's order.
func TestWatchesScreenHasThreeTables(t *testing.T) {
	m := onWatches(t, threeTableStore())
	out := m.View()
	over, under, unpriced := strings.Index(out, "OVERS"),
		strings.Index(out, "UNDERS"), strings.Index(out, "UNPRICED")
	if over < 0 || under < 0 || unpriced < 0 {
		t.Fatalf("want three headings OVERS/UNDERS/UNPRICED, got %d/%d/%d:\n%s",
			over, under, unpriced, out)
	}
	if !(over < under && under < unpriced) {
		t.Errorf("headings out of order: OVERS@%d UNDERS@%d UNPRICED@%d:\n%s",
			over, under, unpriced, out)
	}
}

// THE NEGATIVE CONTROL: each row renders under its own heading and nowhere
// else. An over listed under UNDERS, or an unpriced holding listed as a
// watch, is the way this change fails.
func TestWatchesTablesDoNotLeakRows(t *testing.T) {
	m := onWatches(t, threeTableStore())
	out := m.View()
	at := func(s string) int { return strings.Index(out, s) }
	for _, s := range []string{"OVERS", "UNDERS", "UNPRICED", "Overseer", "Underling", "Nopricehere"} {
		if at(s) < 0 {
			t.Fatalf("%q missing from the screen:\n%s", s, out)
		}
	}
	if strings.Count(out, "Overseer") != 1 || strings.Count(out, "Underling") != 1 ||
		strings.Count(out, "Nopricehere") != 1 {
		t.Errorf("a row rendered in more than one table:\n%s", out)
	}
	if !(at("OVERS") < at("Overseer") && at("Overseer") < at("UNDERS")) {
		t.Errorf("Overseer is not inside the OVERS table:\n%s", out)
	}
	if !(at("UNDERS") < at("Underling") && at("Underling") < at("UNPRICED")) {
		t.Errorf("Underling is not inside the UNDERS table:\n%s", out)
	}
	if at("UNPRICED") > at("Nopricehere") {
		t.Errorf("Nopricehere is not inside the UNPRICED table:\n%s", out)
	}
}

// The unpriced view is gone from the 'v' cycle: four presses from holdings
// come back to holdings.
func TestUnpricedIsNoLongerItsOwnView(t *testing.T) {
	m := onWatches(t, threeTableStore())
	(&m).showView(viewHoldings)
	seen := []string{}
	for range 4 {
		m = key(m, "v")
		seen = append(seen, m.view.String())
	}
	if m.view != viewHoldings {
		t.Errorf("four v presses landed on %v, want back at holdings · saw %v", m.view, seen)
	}
	for _, s := range seen {
		if s == "unpriced" {
			t.Errorf("the v cycle still stops on a standalone unpriced view: %v", seen)
		}
	}
}

// An empty table keeps its heading over a note: two empty sections must
// read as empty tables, not as a broken screen.
func TestWatchesEmptyTablesKeepTheirHeadings(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{{
		ScryfallID: "unpriced-id", Name: "Nopricehere", SetCode: "ccc",
		CollectorNumber: "3", Finish: "foil", Copies: 2, HeldIn: "Collection",
	}}
	m := onWatches(t, st)
	out := m.View()
	for _, s := range []string{"OVERS", "UNDERS", "UNPRICED", "Nopricehere"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing with only unpriced rows present:\n%s", s, out)
		}
	}
}

// `/` narrows all three tables through the same grammar the holdings pane
// uses — the defect fixed for movers in 1515cd1, extended here.
func TestWatchesFilterNarrowsEveryTable(t *testing.T) {
	m := onWatches(t, threeTableStore())
	m = typeFilter(m, "under")
	if m.filterErr != "" {
		t.Fatalf("filterErr = %q", m.filterErr)
	}
	out := m.View()
	if strings.Contains(out, "Overseer") || strings.Contains(out, "Nopricehere") {
		t.Errorf("the query did not narrow the other two tables:\n%s", out)
	}
	if !strings.Contains(out, "Underling") {
		t.Errorf("the matching row is gone:\n%s", out)
	}
	if got := m.filterMatchCount(); got != 1 {
		t.Errorf("match count = %d, want 1 across the three tables", got)
	}
}

// A term no row on this screen can answer says so rather than quietly
// selecting nothing — the unsupportedOnMovers idiom.
func TestWatchesFilterRefusesValueTerm(t *testing.T) {
	m := onWatches(t, threeTableStore())
	m = typeFilter(m, "value>1")
	if m.filterErr != "" {
		t.Fatalf("value>1 must parse — it is a real key: %s", m.filterErr)
	}
	if out := m.View(); !strings.Contains(out, "does not apply on the watches screen") {
		t.Errorf("the bar must name the term this screen cannot answer:\n%s", out)
	}
}

// The cursor spans three tables of two different row types, so the
// selection helpers must disambiguate: a watch row is a watch, an unpriced
// row is not, and enter opens the card either way.
func TestWatchesCursorDisambiguatesRowTypes(t *testing.T) {
	m := onWatches(t, threeTableStore())
	if w := m.selectedWatch(); w == nil || w.Name != "Overseer" {
		t.Fatalf("selectedWatch at rest = %v, want the first over", w)
	}
	if r := m.selectedUnpricedRow(); r != nil {
		t.Errorf("selectedUnpricedRow on a watch row = %+v, want nil", r)
	}
	m = key(m, "down") // Underling
	m = key(m, "down") // Nopricehere
	if w := m.selectedWatch(); w != nil {
		t.Errorf("selectedWatch on an unpriced row = %+v, want nil", w)
	}
	r := m.selectedUnpricedRow()
	if r == nil || r.Name != "Nopricehere" {
		t.Fatalf("selectedUnpricedRow = %v, want Nopricehere", r)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter on an unpriced row did not open the card detail")
	}
}

// The data model puts no card in one table only: watches key on
// (scryfall_id, finish, op), so one printing can carry both a rise alert and
// a fall alert at once. Both are real, and each belongs under its own
// heading — one row in OVERS and one in UNDERS, not a duplicate.
func TestOneCardCanSitInBothWatchTables(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchOn("Bitterblossom", "Bitterblossom-id", "uma", "over", 60, price(34)),
		watchOn("Bitterblossom", "Bitterblossom-id", "uma", "under", 20, price(34)),
	}
	m := onWatches(t, st)
	if len(m.overs) != 1 || len(m.unders) != 1 {
		t.Fatalf("split = %d over, %d under, want one each", len(m.overs), len(m.unders))
	}
	out := m.View()
	if got := strings.Count(out, "Bitterblossom"); got != 2 {
		t.Errorf("card rendered %d times, want once per table:\n%s", got, out)
	}
	if got := strings.Count(out, "$60.00"); got != 1 {
		t.Errorf("the rise threshold rendered %d times, want once", got)
	}
	if got := strings.Count(out, "$20.00"); got != 1 {
		t.Errorf("the fall threshold rendered %d times, want once", got)
	}
	// Both rows are reachable and each is its own watch — removing one must
	// not be ambiguous about which.
	if w := m.selectedWatch(); w == nil || w.Threshold != 60 {
		t.Fatalf("cursor at rest = %v, want the over", w)
	}
	m = key(m, "down")
	if w := m.selectedWatch(); w == nil || w.Threshold != 20 {
		t.Errorf("second row = %v, want the under", w)
	}
}

// Each table keeps its own sort: 's' acts on the table the cursor is in and
// leaves the other two exactly as they were. Before the three-table screen,
// one sort spoke for one list; this is the property that replaces it.
func TestWatchSortIsPerTable(t *testing.T) {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchOn("Zed", "zed-id", "aaa", "over", 5, price(9)),
		watchOn("Abe", "abe-id", "bbb", "over", 5, price(9)),
		watchOn("Zoe", "zoe-id", "ccc", "under", 50, price(9)),
		watchOn("Ann", "ann-id", "ddd", "under", 50, price(9)),
	}
	m := onWatches(t, st)
	before := []string{m.unders[0].Name, m.unders[1].Name}

	// The cursor starts in OVERS; sort it by name.
	if sec, _ := m.watchCursorPos(); sec != secOvers {
		t.Fatalf("cursor starts in %v, want OVERS", sec)
	}
	m = key(m, "s") // state → name
	if m.sortLabel() != "OVERS · name" {
		t.Fatalf("label = %q, want the sorted table named", m.sortLabel())
	}
	if m.overs[0].Name != "Abe" {
		t.Errorf("OVERS by name = %s first, want Abe", m.overs[0].Name)
	}
	if m.unders[0].Name != before[0] || m.unders[1].Name != before[1] {
		t.Errorf("UNDERS moved under a sort aimed at OVERS: %v → %v/%v",
			before, m.unders[0].Name, m.unders[1].Name)
	}
	// ] moves to UNDERS, and 's' there leaves OVERS alone.
	m = key(m, "]")
	if sec, _ := m.watchCursorPos(); sec != secUnders {
		t.Fatalf("] landed in %v, want UNDERS", sec)
	}
	m = key(m, "s")
	if m.sortLabel() != "UNDERS · name" || m.unders[0].Name != "Ann" {
		t.Errorf("UNDERS by %s = %s first, want Ann", m.sortLabel(), m.unders[0].Name)
	}
	if m.overs[0].Name != "Abe" {
		t.Errorf("OVERS lost its own order: %s first", m.overs[0].Name)
	}
	// And ] again reaches the third table, whose cycle is a different one.
	m = key(m, "]")
	if sec, _ := m.watchCursorPos(); sec != secUnpriced {
		t.Errorf("] landed in %v, want UNPRICED", sec)
	}
}
