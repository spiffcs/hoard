package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func openHeldDetail(t *testing.T, st Store) Model {
	t.Helper()
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail did not open")
	}
	m = key(m, "up")
	if m.detail.zone != zoneHeld {
		t.Fatalf("zone = %d, want the held zone", m.detail.zone)
	}
	return m
}

func namesPurchasePrice(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, "purchase") || strings.Contains(l, "paid") ||
		strings.Contains(l, "cost")
}

func TestDetailHeldPurchasePriceEdit(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Condition: store.ConditionNM, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}

	var found, labels = -1, []string{}
	for field := range heldFieldCount {
		m := openHeldDetail(t, st)
		m.detail.heldField = field
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		if m.prompt == nil {
			continue
		}
		labels = append(labels, m.prompt.label)
		if namesPurchasePrice(m.prompt.label) {
			found = field
		}
	}
	if found < 0 {
		t.Fatalf("no held field on the card detail page prompts for the purchase price; "+
			"labels were %q", labels)
	}

	m := openHeldDetail(t, st)
	m.detail.heldField = found
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	m.prompt.text = "12.50"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if st.setPurchasePrice != "Bitterblossom-id→12.50" {
		t.Errorf("store saw %q, want the edited purchase price to reach it",
			st.setPurchasePrice)
	}
	if m.undoStack == nil {
		t.Error("a purchase price change recorded no undo")
	}
}
