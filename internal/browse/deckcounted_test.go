package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/store"
)

func deckPaneModel(t *testing.T) Model {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return next.(Model)
}

func cursorOn(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, c := range m.containers {
		if c.Name == name {
			m.focus = paneContainers
			m.cursor[paneContainers] = i
			return m
		}
	}
	t.Fatalf("no container named %q", name)
	return m
}

func TestTogglingADeckOutOfTheCollection(t *testing.T) {
	m := cursorOn(t, deckPaneModel(t), "Rich Deck")

	sel := m.selectedContainer()
	if sel == nil || sel.Kind != store.KindDeck {
		t.Fatalf("setup: selected %+v, want a deck", sel)
	}
	if !sel.Counted {
		t.Fatal("setup: the deck should start counted")
	}

	m = key(m, "x")
	if got := m.selectedContainer(); got == nil || got.Counted {
		t.Errorf("x did not take the deck out of the collection: %+v", got)
	}
	if !strings.Contains(m.status, "not counted") {
		t.Errorf("status = %q, want it to say the deck is no longer counted", m.status)
	}

	m = key(m, "x")
	if got := m.selectedContainer(); got == nil || !got.Counted {
		t.Errorf("x did not put the deck back: %+v", got)
	}
}

func TestUncountedDeckIsFadedAndMarked(t *testing.T) {
	m := deckPaneModel(t)
	before := containerLineFor(t, m, "Rich Deck")

	if err := m.store.SetContainerCounted(202, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()

	after := containerLineFor(t, m, "Rich Deck")
	if after == before {
		t.Fatalf("an excluded deck renders identically to a counted one:\n%q", after)
	}
	if !strings.Contains(after, "\x1b[2m") {
		t.Errorf("excluded deck is not faded:\n%q", after)
	}
	if !strings.Contains(after, settlingMark) {
		t.Errorf("excluded deck carries no marker:\n%q", after)
	}
	if other := containerLineFor(t, m, "Cheap Deck"); strings.Contains(other, settlingMark) {
		t.Errorf("a counted deck picked up the marker:\n%q", other)
	}
}

func TestGutterOffersTheCountToggleOnADeck(t *testing.T) {
	m := cursorOn(t, deckPaneModel(t), "Rich Deck")
	if help := m.helpLine(); !strings.Contains(help, "x exclude") {
		t.Errorf("a counted deck does not offer to exclude it:\n%s", help)
	}

	if err := m.store.SetContainerCounted(202, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()
	m = cursorOn(t, m, "Rich Deck")
	if help := m.helpLine(); !strings.Contains(help, "x include") {
		t.Errorf("an excluded deck does not offer to put it back:\n%s", help)
	}
}

func TestAllCardsLeavesOutAnUncountedDeck(t *testing.T) {
	m := deckPaneModel(t)
	all := m.containers[0]
	if all.Kind != kindAllCards {
		t.Fatalf("setup: container 0 is %+v, want All Cards", all)
	}
	if all.Value != 850 || all.Copies != 208 {
		t.Fatalf("setup: All Cards = %d copies worth %v, want 208 and 850",
			all.Copies, all.Value)
	}

	if err := m.store.SetContainerCounted(202, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()

	all = m.containers[0]
	if all.Value != 350 {
		t.Errorf("All Cards value = %v, want 350 — the $500 deck must not count", all.Value)
	}
	if all.Copies != 108 {
		t.Errorf("All Cards copies = %d, want 108 — the deck's 100 must not count", all.Copies)
	}
}
