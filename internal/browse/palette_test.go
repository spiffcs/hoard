package browse

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
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

func TestPaletteDrawerGeometry(t *testing.T) {
	m := newTestModel(t, testStore())
	rows := m.visibleRows()
	closedHelp := m.helpRows()
	closedFrame := strings.Count(m.View(), "\n")
	m = key(m, ":")

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

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.palette == nil {
		t.Error("enter on no matches closed the palette")
	}
}

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
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

func TestMoversPaletteRanking(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34)}
	m := atAllCards(t, newTestModel(t, st))
	m.opRepairFinishes = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	m = key(m, "v")
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

func TestWatchesPaletteRanking(t *testing.T) {
	st := testStore()
	w := store.WatchStatus{Name: "Bitterblossom"}
	w.ScryfallID, w.Finish, w.Op, w.Threshold = "Bitterblossom-id", finish.Nonfoil, "<=", 30
	st.watches = []store.WatchStatus{w}
	m := atAllCards(t, newTestModel(t, st))
	m.opWatchAdd = func(ctx context.Context, p progress.Fn, name, op string, threshold float64) (string, error) {
		return "", nil
	}
	m.opRepairFinishes = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	for range 3 {
		m = key(m, "v")
	}
	if m.view != viewWatches {
		t.Fatalf("view = %v, want watches", m.view)
	}
	m.openPalette()
	pickAt, byNameAt, repairAt := -1, -1, -1
	for i, match := range m.palette.matches {
		switch m.commands[match.index].id {
		case "watch.pick":
			pickAt = i
		case "watch.add-by-name":
			byNameAt = i
		case "op.repair-finishes":
			repairAt = i
		case "remove", "undo":
			t.Errorf("%s offered on watches", m.commands[match.index].id)
		}
	}

	if repairAt == -1 {
		t.Error("RepairFinishes must be offered where the unpriced table lives")
	}
	if pickAt == -1 || byNameAt == -1 || byNameAt != pickAt+1 {
		t.Errorf("AddWatchFromCollection at %d, AddWatchForAnyCard at %d — want them adjacent, picker first",
			pickAt, byNameAt)
	}

	m.palette = nil
	m = key(m, "d")
	if m.confirm == nil {
		t.Error("d on watches must still stage the watch removal")
	}
}

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
