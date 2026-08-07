package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
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
