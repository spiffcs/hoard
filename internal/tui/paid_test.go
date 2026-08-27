package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func atPaidStep(t *testing.T, ra *recordingAdder) model {
	t.Helper()
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Ulamog", nil)
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty", got.state)
	}
	got.qtyInput.SetValue("3")
	mm, _ = got.submitQty()
	got = mm.(model)
	if got.state == stateConfirm {
		t.Fatalf("quantity went straight to confirm; the picker never asks what you paid")
	}
	return got
}

func TestPickerAsksWhatYouPaidAndSaysHowToSkip(t *testing.T) {
	got := atPaidStep(t, &recordingAdder{})

	view := got.View()
	low := strings.ToLower(view)
	if !strings.Contains(low, "paid") {
		t.Errorf("the step does not say it is asking what you paid:\n%s", view)
	}
	if !strings.Contains(low, "skip") {
		t.Errorf("the step does not tell the user how to skip it:\n%s", view)
	}
}

func TestPickerSkipsCostBasisOnAnEmptyEntry(t *testing.T) {
	ra := &recordingAdder{}
	got := atPaidStep(t, ra)

	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm after skipping", got.state)
	}

	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	if p := ra.got[0].PurchasePrice; p != nil {
		t.Errorf("skipping recorded a cost basis of %v; it must record none", *p)
	}
	if ra.got[0].Qty != 3 {
		t.Errorf("qty = %d, want the 3 chosen earlier", ra.got[0].Qty)
	}
}

func TestPickerRecordsWhatYouPaidWhenGiven(t *testing.T) {
	ra := &recordingAdder{}
	got := atPaidStep(t, ra)

	var mm tea.Model = got
	for _, r := range "12.50" {
		mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", got.state)
	}

	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	p := ra.got[0].PurchasePrice
	if p == nil || *p != 12.50 {
		t.Errorf("purchase price = %v, want 12.50", p)
	}
}
