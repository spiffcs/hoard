package browse

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

func openTestPalette(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, testStore())
	m = key(m, ":")
	if m.palette == nil {
		t.Fatal("':' did not open the palette")
	}
	return m
}

// The empty query lists every applicable, visible command — the hidden key
// reflexes (sort, floor, view cycling) never appear; typing narrows by fuzzy
// match; enter runs the selection and closes the drawer.
func TestPaletteNarrowsAndRuns(t *testing.T) {
	m := openTestPalette(t)
	visible := 0
	for i := range m.commands {
		if !m.commands[i].hidden && m.commands[i].applies(&m) {
			visible++
		}
	}
	if visible == len(m.commands) {
		t.Fatal("no hidden commands left — this test no longer proves the filter")
	}
	if len(m.palette.matches) != visible {
		t.Errorf("empty query shows %d commands, want every visible one (%d)",
			len(m.palette.matches), visible)
	}

	for _, r := range "reload" {
		m = key(m, string(r))
	}
	if len(m.palette.matches) == 0 {
		t.Fatal("query 'reload' matched nothing")
	}
	top := m.commands[m.palette.matches[0].index]
	if top.id != "reload" {
		t.Errorf("top match = %s, want reload", top.id)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.palette != nil {
		t.Error("palette still open after enter")
	}
	if m.status != "reloaded" {
		t.Errorf("status = %q, want the reload receipt", m.status)
	}
}

// Hidden commands stay bound: their keys work exactly as before.
func TestHiddenCommandsKeepTheirKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m = key(m, "s")
	if !strings.HasPrefix(m.status, "sorted by ") {
		t.Errorf("status = %q, want the sort receipt from the hidden command", m.status)
	}
	m = key(m, "v")
	if m.view != viewMovers {
		t.Errorf("view = %v, want movers from the hidden view-cycle command", m.view)
	}
}

// The highlighted command's description renders under the help line — and
// costs a chrome row, so the frame keeps its height.
func TestPaletteShowsSelectedDescription(t *testing.T) {
	m := openTestPalette(t)
	desc := m.paletteDesc()
	if desc == "" {
		t.Fatal("the first palette command has no description")
	}
	if out := m.View(); !strings.Contains(out, desc) {
		t.Errorf("view does not show the highlighted description %q", desc)
	}
	if h1, h2 := strings.Count(m.View(), "\n"), func() int {
		m2 := newTestModel(t, testStore())
		return strings.Count(m2.View(), "\n")
	}(); h1 != h2 {
		t.Errorf("frame height changed with the palette open: %d vs %d", h1, h2)
	}
}

func TestPaletteEscClosesWithoutRunning(t *testing.T) {
	m := openTestPalette(t)
	before := m.view
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.palette != nil || m.view != before {
		t.Errorf("esc: palette=%v view=%v, want closed and unchanged", m.palette, m.view)
	}
}

// The drawer takes its rows from the panes so the frame keeps its height,
// and the input line owns the status slot.
func TestPaletteDrawerGeometry(t *testing.T) {
	m := newTestModel(t, testStore())
	rows := m.visibleRows()
	closedHelp := m.helpRows()
	closedFrame := strings.Count(m.View(), "\n")
	m = key(m, ":")
	// The drawer takes its rows from the panes; the help line may cost a
	// different number of rows in palette mode (its help is shorter), so
	// the expectation accounts for both.
	if got, want := m.visibleRows(), rows-m.paletteRows()+closedHelp-m.helpRows(); got != want {
		t.Errorf("visibleRows = %d with palette open, want %d", got, want)
	}
	if !strings.HasPrefix(m.statusLine(), ": ") {
		t.Errorf("status slot = %q, want the palette input", m.statusLine())
	}
	out := m.View()
	if !strings.Contains(out, "▸ ") {
		t.Errorf("view has no palette cursor row:\n%s", out)
	}
	// The drawer takes rows from the panes, so the frame height is
	// unchanged — the invariant that keeps the screen from jumping.
	if got := strings.Count(out, "\n"); got != closedFrame {
		t.Errorf("frame is %d newlines with the drawer open, want %d (same as closed)", got, closedFrame)
	}
}

func TestPaletteNoMatchRow(t *testing.T) {
	m := openTestPalette(t)
	for _, r := range "zzzzqqq" {
		m = key(m, string(r))
	}
	if len(m.palette.matches) != 0 {
		t.Fatalf("nonsense query matched %d commands", len(m.palette.matches))
	}
	if !strings.Contains(m.View(), "no matching command") {
		t.Error("empty result row missing")
	}
	// Enter on nothing does nothing.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.palette == nil {
		t.Error("enter on no matches closed the palette")
	}
}

// ctrl+p is the unadvertised alias, and ':' works from the detail overlay
// (closing it — context commands come later).
func TestPaletteAliasAndDetail(t *testing.T) {
	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(Model)
	if m.palette == nil {
		t.Fatal("ctrl+p did not open the palette")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	m = key(m, "tab")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("setup: detail did not open")
	}
	m = key(m, ":")
	if m.palette == nil || m.detail == nil {
		t.Errorf("':' from detail: palette=%v detail=%v, want the palette over the still-open overlay",
			m.palette, m.detail)
	}
}

// Direct keys run through the registry: the palette's hint column and the
// real bindings share one definition, checked here for one exemplar.
func TestRegistryKeyParity(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "v")
	if m.view != viewMovers {
		t.Errorf("view after 'v' = %v, want movers — the registry key did not run", m.view)
	}
	for _, c := range m.commands {
		if c.id == "view.next" && c.key != "v" {
			t.Errorf("registry key for view.next = %q — the hint column would lie", c.key)
		}
	}
}

// Over the card detail the palette narrows to the price refreshers: the
// overlay is a reading surface, and every other verb waits an esc away.
func TestDetailPaletteOffersOnlyPriceRefreshers(t *testing.T) {
	m := newTestModel(t, testStore())
	m.opUpdatePrices = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	m.openPalette()
	if m.palette == nil || len(m.palette.matches) == 0 {
		t.Fatal("no palette matches over the detail")
	}
	for _, match := range m.palette.matches {
		if id := m.commands[match.index].id; !detailPaletteIDs[id] {
			t.Errorf("palette offers %q over the detail, want price refreshers only", id)
		}
	}
}

// Spaced queries keep matching PascalCase titles: the palette derives the
// spaced words from each title, so "update prices" finds UpdatePrices
// without every command restating its own name in aliases.
// The registry search targets stay spaced-query friendly end to end.
func TestPaletteMatchesSpacedQuery(t *testing.T) {
	m := newTestModel(t, testStore())
	m.opUpdatePrices = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	m.openPalette()
	m.palette.Query = "update prices"
	m.refreshPalette()
	found := false
	for _, match := range m.palette.matches {
		if m.commands[match.index].id == "op.update-prices" {
			found = true
		}
	}
	if !found {
		t.Error(`query "update prices" must match UpdatePrices`)
	}
}

// The movers palette drops RepairFinishes — every row there is priced, so
// the repair has nothing to fix — and ranks WatchThisCard above the
// everyday holdings verbs like AddCards.
func TestMoversPaletteRanking(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", "nonfoil", 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m.opRepairFinishes = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	m = key(m, "v") // movers
	m.openPalette()
	watchAt, addAt := -1, -1
	for i, match := range m.palette.matches {
		switch m.commands[match.index].id {
		case "watch.add":
			watchAt = i
		case "add":
			addAt = i
		case "op.repair-finishes":
			t.Error("RepairFinishes offered on movers")
		}
	}
	if watchAt == -1 || addAt == -1 || watchAt > addAt {
		t.Errorf("WatchThisCard at %d, AddCards at %d — want the watch ranked above", watchAt, addAt)
	}
}

// The watches palette keeps the two add-a-watch verbs together — the
// collection picker leads, the by-name fallback sits directly under it —
// and drops the holdings-editing verbs (their keys stay bound).
func TestWatchesPaletteRanking(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Bitterblossom"}
	w.ScryfallID, w.Finish, w.Op, w.Threshold = "Bitterblossom-id", "nonfoil", "<=", 30
	st.watches = []store.WatchStatus{w}
	m := atAllCards(t, newTestModel(t, st))
	m.opWatchAdd = func(ctx context.Context, p progress.Fn, name, op string, threshold float64) (string, error) {
		return "", nil
	}
	m.opRepairFinishes = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	for range 3 {
		m = key(m, "v") // movers → unpriced → watches
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	m.openPalette()
	pickAt, byNameAt := -1, -1
	for i, match := range m.palette.matches {
		switch m.commands[match.index].id {
		case "watch.pick":
			pickAt = i
		case "watch.add-by-name":
			byNameAt = i
		case "remove", "undo", "op.repair-finishes":
			t.Errorf("%s offered on watches", m.commands[match.index].id)
		}
	}
	if pickAt == -1 || byNameAt == -1 || byNameAt != pickAt+1 {
		t.Errorf("AddWatchFromCollection at %d, AddWatchForAnyCard at %d — want them adjacent, picker first",
			pickAt, byNameAt)
	}

	// Hidden from the listing, not unbound: d still stages the removal of
	// the watch under the cursor.
	m.palette = nil
	m = key(m, "d")
	if m.confirm == nil {
		t.Error("d on watches must still stage the watch removal")
	}
}

// The market palette drops the holdings-editing verbs too: nothing on the
// vendor sheets is a row you'd remove or an edit you'd undo.
func TestMarketPaletteDropsEditVerbs(t *testing.T) {
	m := newTestModel(t, testStore())
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m.openPalette()
	for _, match := range m.palette.matches {
		if id := m.commands[match.index].id; id == "remove" || id == "undo" {
			t.Errorf("%s offered on market", id)
		}
	}
}

// BrowseBySets lists in the palette with its key hint, and runs.
func TestPaletteListsBrowseBySets(t *testing.T) {
	m := openTestPalette(t)
	found := false
	for _, match := range m.palette.matches {
		if c := m.commands[match.index]; c.id == "sets.toggle" {
			found = c.title == "BrowseBySets" && c.key == "B"
		}
	}
	if !found {
		t.Fatal("BrowseBySets missing from the palette (or wrong title/key)")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	m = key(m, "B")
	if !m.setsMode {
		t.Error("B did not toggle sets mode")
	}
}
