package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func watchOn(name, id, set, op string, threshold float64, price *float64) store.WatchStatus {
	w := store.WatchStatus{Name: name, SetCode: set, CollectorNumber: "1", PriceUSD: price}
	w.ScryfallID, w.Finish, w.Op, w.Threshold = id, finish.Nonfoil, op, threshold
	return w
}

func shownWatches(m Model) []store.WatchStatus {
	return append(append([]store.WatchStatus(nil), m.overs...), m.unders...)
}

func threeTableStore() *fakeStore {
	st := testStore()
	st.watches = []store.WatchStatus{
		watchOn("Overseer", "over-id", "aaa", "over", 5, price(9)),
		watchOn("Underling", "under-id", "bbb", "under", 50, price(9)),
	}
	st.unpriced = []store.UnpricedRow{{
		ScryfallID: "unpriced-id", Name: "Nopricehere", SetCode: "ccc",
		CollectorNumber: "3", Finish: finish.Foil, Copies: 2, HeldIn: "Collection",
	}}
	return st
}

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

func TestUnpricedIsNoLongerItsOwnView(t *testing.T) {
	m := onWatches(t, threeTableStore())
	(&m).showView(viewHoldings)
	seen := []string{}
	for range 5 {
		m = key(m, "v")
		seen = append(seen, m.view.String())
	}
	if m.view != viewHoldings {
		t.Errorf("five v presses landed on %v, want back at holdings · saw %v", m.view, seen)
	}
	for _, s := range seen {
		if s == "unpriced" {
			t.Errorf("the v cycle still stops on a standalone unpriced view: %v", seen)
		}
	}
}

func TestWatchesEmptyTablesKeepTheirHeadings(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{{
		ScryfallID: "unpriced-id", Name: "Nopricehere", SetCode: "ccc",
		CollectorNumber: "3", Finish: finish.Foil, Copies: 2, HeldIn: "Collection",
	}}
	m := onWatches(t, st)
	out := m.View()
	for _, s := range []string{"OVERS", "UNDERS", "UNPRICED", "Nopricehere"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing with only unpriced rows present:\n%s", s, out)
		}
	}
}

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

func TestWatchesCursorDisambiguatesRowTypes(t *testing.T) {
	m := onWatches(t, threeTableStore())
	if w := m.selectedWatch(); w == nil || w.Name != "Overseer" {
		t.Fatalf("selectedWatch at rest = %v, want the first over", w)
	}
	if r := m.selectedUnpricedRow(); r != nil {
		t.Errorf("selectedUnpricedRow on a watch row = %+v, want nil", r)
	}
	m = key(m, "down")
	m = key(m, "down")
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

	if w := m.selectedWatch(); w == nil || w.Threshold != 60 {
		t.Fatalf("cursor at rest = %v, want the over", w)
	}
	m = key(m, "down")
	if w := m.selectedWatch(); w == nil || w.Threshold != 20 {
		t.Errorf("second row = %v, want the under", w)
	}
}

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

	if sec, _ := m.watchCursorPos(); sec != secOvers {
		t.Fatalf("cursor starts in %v, want OVERS", sec)
	}
	m = key(m, "s")
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

	m = key(m, "]")
	if sec, _ := m.watchCursorPos(); sec != secUnpriced {
		t.Errorf("] landed in %v, want UNPRICED", sec)
	}
}
