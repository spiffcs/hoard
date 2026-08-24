package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func binderPaneModel(t *testing.T) Model {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	f := testStore()
	f.binders = map[int64]string{77: "Want"}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m.focus = paneContainers
	m.cursor[paneContainers] = 0
	return m
}

func containerLineFor(t *testing.T, m Model, name string) string {
	t.Helper()
	for _, l := range m.containerLines(30) {
		if strings.Contains(l, name) {
			return l
		}
	}
	t.Fatalf("no container row for %q in:\n%s", name, strings.Join(m.containerLines(30), "\n"))
	return ""
}

func TestUncountedBinderIsFadedAndMarked(t *testing.T) {
	m := binderPaneModel(t)
	before := containerLineFor(t, m, "Want")

	if err := m.store.SetContainerCounted(77, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()

	after := containerLineFor(t, m, "Want")
	if after == before {
		t.Fatalf("an excluded binder renders identically to a counted one:\n%q", after)
	}
	if !strings.Contains(after, "\x1b[2m") {
		t.Errorf("excluded binder is not faded:\n%q", after)
	}
	if !strings.Contains(after, settlingMark) {
		t.Errorf("excluded binder carries no marker:\n%q", after)
	}
	if other := containerLineFor(t, m, "Binder"); strings.Contains(other, settlingMark) {
		t.Errorf("a counted binder picked up the marker:\n%q", other)
	}
}

func TestGutterOffersTheCountToggleOnABinderOnly(t *testing.T) {
	m := binderPaneModel(t)

	m.cursor[paneContainers] = 0
	if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
		t.Fatalf("setup: cursor 0 is %+v, want All Cards", sel)
	}
	if help := m.helpLine(); strings.Contains(help, "x ") {
		t.Errorf("All Cards offers the count toggle:\n%s", help)
	}

	m.cursor[paneContainers] = 1
	if help := m.helpLine(); !strings.Contains(help, "x exclude") {
		t.Errorf("a counted binder does not offer to exclude it:\n%s", help)
	}

	if err := m.store.SetContainerCounted(defaultBinderID, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()
	m.cursor[paneContainers] = 1
	if help := m.helpLine(); !strings.Contains(help, "x include") {
		t.Errorf("an excluded binder does not offer to put it back:\n%s", help)
	}
}
