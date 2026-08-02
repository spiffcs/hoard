package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
