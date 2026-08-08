package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// parseCondition accepts more than it stores, on purpose. Somebody typing here
// has just read "Lightly Played" off a seller's listing, and refusing the words
// in favour of the two-letter code would be pedantry.
func TestParseCondition(t *testing.T) {
	for in, want := range map[string]string{
		// The stored vocabulary.
		"nm": "nm", "lp": "lp", "mp": "mp", "hp": "hp", "dmg": "dmg",
		// The words the marketplaces spell it with.
		"Near Mint":         "nm",
		"Lightly Played":    "lp",
		"Slightly Played":   "lp",
		"Excellent":         "lp",
		"Moderately Played": "mp",
		"Played":            "mp",
		"Heavily Played":    "hp",
		"Damaged":           "dmg",
		"Poor":              "dmg",
		// Unassessed, however it is expressed — the display dash included, so
		// what the row shows can be typed straight back.
		"-": "unknown", "": "unknown", "unknown": "unknown", "?": "unknown",
	} {
		got, err := parseCondition(in)
		if err != nil {
			t.Errorf("parseCondition(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseCondition(%q) = %q, want %q", in, got, want)
		}
	}
	// A grade is a different concept and is not silently rounded to a
	// condition; the prompt says what it accepts.
	for _, bad := range []string{"PSA 10", "BGS 9.5", "pristine", "gem mint"} {
		if _, err := parseCondition(bad); err == nil {
			t.Errorf("parseCondition(%q) = nil error, want a refusal", bad)
		}
	}
}

// The prompt prefills with the condition to change, and an unassessed row
// starts empty rather than with the word "unknown": the field is being asked to
// state something, and the commonest answer is a condition.
func TestConditionInputPrefill(t *testing.T) {
	if got := conditionInput("unknown"); got != "" {
		t.Errorf("conditionInput(unknown) = %q, want empty", got)
	}
	if got := conditionInput(""); got != "" {
		t.Errorf("conditionInput(zero) = %q, want empty", got)
	}
	if got := conditionInput("lp"); got != "lp" {
		t.Errorf("conditionInput(lp) = %q, want lp", got)
	}
}

// The condition editor end to end: ←/→ reaches the field, enter opens a prompt
// prefilled with what the row shows, and committing calls the store's mover
// with the row's own condition on both sides.
func TestDetailHeldConditionEdit(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: "nonfoil", Condition: store.ConditionUnknown, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "tab") // focus the cards pane
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail did not open")
	}
	m = key(m, "up") // climb into the held zone
	if m.detail.zone != zoneHeld {
		t.Fatalf("zone = %d, want the held zone", m.detail.zone)
	}

	// → past quantity, printing and finish lands on condition.
	for range 3 {
		m = key(m, "right")
	}
	if m.detail.heldField != fieldCondition {
		t.Fatalf("field = %d, want condition", m.detail.heldField)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("enter on the condition field opened no prompt")
	}
	// An unassessed row prefills empty: the field is being asked to state
	// something, not to re-assert that nobody knows.
	if m.prompt.text != "" {
		t.Errorf("prompt text = %q, want empty for an unassessed row", m.prompt.text)
	}
	if !strings.Contains(m.prompt.label, "condition") {
		t.Errorf("prompt label = %q, want it to name the field", m.prompt.label)
	}

	m.prompt.text = "Lightly Played"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := st.movedCondition; got != "unknown→lp" {
		t.Errorf("store saw %q, want the row's own condition on both sides", got)
	}
	if m.undoStack == nil {
		t.Error("a condition change recorded no undo")
	}
}

// A first run has no cards, and the header still has to name the pane.
//
// The totals hug the table's right edge rather than the pane's, which reads
// well until the table is narrower than the header itself: an empty
// collection's table is nothing but its column titles, eleven columns of
// "NAME  VALUE". Anchoring to that left one column for a seventeen-character
// title, so the very first thing a new user saw was "… 0 · $0.00".
func TestEmptyCollectionHeaderNamesThePane(t *testing.T) {
	st := &fakeStore{
		binders:    map[int64]string{},
		binderRows: map[int64][]store.CollectionRow{},
	}
	m, err := New(st, WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	header := strings.Split(m.View(), "\n")[0]
	if !strings.Contains(header, "CARDS · ALL CARDS") {
		t.Errorf("header = %q, want the pane named in full", header)
	}
	if strings.Contains(header, "…") {
		t.Errorf("header truncated the title away: %q", header)
	}
	// The totals still ride alongside rather than being pushed off.
	if !strings.Contains(header, "0 · $0.00") {
		t.Errorf("header = %q, want the totals kept", header)
	}
}

// The totals still hug the table's edge when the table is wide enough to
// deserve it — the behaviour the anchor exists for, which the fix must not
// trade away for the empty case.
func TestHeaderTotalsStillHugAWideTable(t *testing.T) {
	m := newTestModel(t, testStore())
	header := strings.Split(m.View(), "\n")[0]
	_, right := m.paneWidths()
	if got := lipgloss.Width(strings.TrimRight(ansi.Strip(header), " ")); got >= right+containerPaneWidth+paneGap {
		t.Errorf("header spans %d columns, want the totals short of the pane's far edge:\n%q",
			got, header)
	}
}

// Every held edit re-reads the overlay, and reloadDetail's comp refetch is
// part of that re-read: with the memo empty, loadPrinting marks the sheet
// pending and only the returned command clears it. The edit paths discarded
// it — the COMPS section sat on "reading today's vendor quotes…" with no
// read in flight.
func TestHeldEditCarriesTheCompsRefetch(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: "nonfoil", Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m.cardComps = func(id string) (map[string]market.Comp, bool) {
		return map[string]market.Comp{"nonfoil": {}}, true
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open; the fetch cmd is dropped on purpose
	m = next.(Model)
	if m.detail == nil || !m.detail.compsPending {
		t.Fatal("setup: want an open detail with its comp sheet pending")
	}
	id := m.detail.card.ScryfallID

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	m = next.(Model)
	if !m.detail.compsPending {
		t.Fatal("setup drift: the edit's reload should re-mark the sheet pending")
	}
	if cmd == nil {
		t.Fatal("the held edit dropped reloadDetail's command — the sheet pends forever")
	}
	msg, ok := cmd().(detailCompsMsg)
	if !ok || msg.scryfallID != id {
		t.Fatalf("edit command yielded %+v, want this printing's comp read", msg)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.detail.compsPending || !m.detail.compsOK {
		t.Errorf("pending %v ok %v after the read landed, want the sheet answered",
			m.detail.compsPending, m.detail.compsOK)
	}
}
