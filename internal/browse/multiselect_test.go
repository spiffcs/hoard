package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/store"
)

func selNames(rows []store.CollectionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

// testStore's binder rows sort by value: Bitterblossom (4 copies, $136),
// Ancient Tomb (1, $134), Sol Ring (3, $30) — 8 copies worth $300 in all.
func onHoldings(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, testStore())
	if m.view != viewHoldings {
		t.Fatalf("view = %v, want holdings", m.view)
	}
	if len(m.cards) != 3 {
		t.Fatalf("cards = %d, want the three fixture rows", len(m.cards))
	}
	m.focus = paneCards
	m.cursor[paneCards] = 0
	return m
}

func TestShiftDownExtendsTheSelectionDownward(t *testing.T) {
	m := onHoldings(t)
	if got := m.selectedRows(); len(got) != 0 {
		t.Fatalf("selection starts with %d rows, want none", len(got))
	}

	m = key(m, "shift+down")
	if got := m.selectedRows(); len(got) != 2 {
		t.Fatalf("one shift+down selected %d rows, want the anchor and the next", len(got))
	}
	m = key(m, "shift+down")
	rows := m.selectedRows()
	if len(rows) != 3 {
		t.Fatalf("two shift+downs selected %d rows, want 3", len(rows))
	}
	if rows[0].Name != "Bitterblossom" || rows[2].Name != "Sol Ring" {
		t.Errorf("selection = %v, want the whole list in order", selNames(rows))
	}
	if m.cursor[paneCards] != 2 {
		t.Errorf("cursor = %d, want it to travel with the selection", m.cursor[paneCards])
	}
}

func TestShiftUpExtendsTheSelectionUpward(t *testing.T) {
	m := onHoldings(t)
	m.cursor[paneCards] = 2

	m = key(m, "shift+up")
	rows := m.selectedRows()
	if len(rows) != 2 {
		t.Fatalf("shift+up selected %d rows, want 2", len(rows))
	}
	if rows[0].Name != "Ancient Tomb" || rows[1].Name != "Sol Ring" {
		t.Errorf("selection = %v, want the range in list order", selNames(rows))
	}
}

func TestSelectionStopsAtTheEnds(t *testing.T) {
	m := onHoldings(t)
	for range 6 {
		m = key(m, "shift+down")
	}
	if got := len(m.selectedRows()); got != 3 {
		t.Errorf("selection ran past the end: %d rows, want 3", got)
	}
	for range 6 {
		m = key(m, "shift+up")
	}
	if got := len(m.selectedRows()); got != 1 {
		t.Errorf("shifting back up left %d rows, want the anchor alone", got)
	}
}

func TestPlainMovementClearsTheSelection(t *testing.T) {
	m := onHoldings(t)
	m = key(m, "shift+down")
	if len(m.selectedRows()) == 0 {
		t.Fatal("setup: nothing selected")
	}

	m = key(m, "down")
	if got := len(m.selectedRows()); got != 0 {
		t.Errorf("a plain arrow left %d rows selected, want the selection dropped", got)
	}
}

func TestEscClearsTheSelection(t *testing.T) {
	m := onHoldings(t)
	m = key(m, "shift+down")
	if len(m.selectedRows()) == 0 {
		t.Fatal("setup: nothing selected")
	}
	m = key(m, "esc")
	if got := len(m.selectedRows()); got != 0 {
		t.Errorf("esc left %d rows selected", got)
	}
}

func TestHeaderReportsCopiesValueAndRows(t *testing.T) {
	m := onHoldings(t)

	_, before := m.viewHeader()
	if strings.Contains(before, "SELECTED") {
		t.Fatalf("header shows a selection before anything is selected: %q", before)
	}

	m = key(m, "shift+down")
	_, totals := m.viewHeader()
	// Bitterblossom (4 copies, $136) + Ancient Tomb (1 copy, $134).
	for _, want := range []string{"SELECTED", "5", "$270.00", "2 rows"} {
		if !strings.Contains(totals, want) {
			t.Errorf("header %q is missing %q", totals, want)
		}
	}

	m = key(m, "shift+down")
	_, all := m.viewHeader()
	for _, want := range []string{"8", "$300.00", "3 rows"} {
		if !strings.Contains(all, want) {
			t.Errorf("header %q is missing %q", all, want)
		}
	}
}

func TestChangingContainerClearsTheSelection(t *testing.T) {
	m := onHoldings(t)
	m = key(m, "shift+down")
	if len(m.selectedRows()) == 0 {
		t.Fatal("setup: nothing selected")
	}

	m.cursor[paneContainers] = 0
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	if got := len(m.selectedRows()); got != 0 {
		t.Errorf("switching containers left %d rows selected", got)
	}
}

func TestAFreshModelHasNoSelection(t *testing.T) {
	m, err := New(testStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, ok := m.selectionRange(); ok {
		t.Error("a model straight out of New reports a selection; the anchor must start unset")
	}
}

func lineFor(t *testing.T, body, name string) string {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	t.Fatalf("no row for %q in:\n%s", name, body)
	return ""
}

func TestSelectedRowsAreVisiblyMarked(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sized := func() Model {
		m := onHoldings(t)
		next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
		m = next.(Model)
		m.focus = paneCards
		return m
	}

	// Cursor parked on row 1 with nothing selected.
	plain := sized()
	plain.cursor[paneCards] = 1
	unselected := lineFor(t, strings.Join(plain.cardLines(110), "\n"), "Bitterblossom")

	// Same cursor position, but reached by extending a selection from row 0.
	picked := sized()
	picked.cursor[paneCards] = 0
	picked = key(picked, "shift+down")
	if picked.cursor[paneCards] != 1 {
		t.Fatalf("setup: cursor = %d, want 1", picked.cursor[paneCards])
	}
	body := strings.Join(picked.cardLines(110), "\n")
	selected := lineFor(t, body, "Bitterblossom")

	// What row 0 looks like when the cursor is sitting on it.
	cursored := sized()
	cursored.cursor[paneCards] = 0
	onCursor := lineFor(t, strings.Join(cursored.cardLines(110), "\n"), "Bitterblossom")

	if selected == unselected {
		t.Fatalf("row 0 is selected but renders exactly as an unselected row:\n%q", selected)
	}
	if selected != onCursor {
		t.Errorf("a selected row does not use the cursor highlight\n got: %q\nwant: %q",
			selected, onCursor)
	}
	if lineFor(t, body, "Sol Ring") != lineFor(t,
		strings.Join(plain.cardLines(110), "\n"), "Sol Ring") {
		t.Error("an unselected row changed appearance")
	}
}

func TestTogglingABinderOutOfTheCollection(t *testing.T) {
	f := testStore()
	f.binders = map[int64]string{}
	m := newTestModel(t, f)
	m.focus = paneContainers
	m.cursor[paneContainers] = 1

	sel := m.selectedContainer()
	if sel == nil || sel.Kind != store.KindCollection {
		t.Fatalf("setup: selected %+v, want a binder", sel)
	}
	if !sel.Counted {
		t.Fatal("setup: the binder should start counted")
	}

	m = key(m, "x")
	if got := m.selectedContainer(); got == nil || got.Counted {
		t.Errorf("x did not take the binder out of the collection: %+v", got)
	}
	if !strings.Contains(m.status, "not counted") {
		t.Errorf("status = %q, want it to say the binder is no longer counted", m.status)
	}

	m = key(m, "x")
	if got := m.selectedContainer(); got == nil || !got.Counted {
		t.Errorf("x did not put the binder back: %+v", got)
	}
}

func (f *fakeStore) SetContainerCounted(id int64, counted bool) error {
	if f.err != nil {
		return f.err
	}
	if f.uncounted == nil {
		f.uncounted = map[int64]bool{}
	}
	f.uncounted[id] = !counted
	return nil
}
