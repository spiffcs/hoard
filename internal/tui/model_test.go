package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cphillips918/mtg_index/internal/scryfall"
)

// fakeSearcher returns canned results for the cascade tests.
type fakeSearcher struct {
	auto   map[string][]string
	prints map[string][]scryfall.Card
}

func (f fakeSearcher) Autocomplete(_ context.Context, q string) ([]string, error) {
	return f.auto[q], nil
}
func (f fakeSearcher) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	return f.prints[name], nil
}

func fp(v float64) *float64 { return &v }

// step executes a single (non-batched) command and feeds its message back into
// the model. The tests that use it only ever return a single lookup command.
func step(t *testing.T, m tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return m, nil
	}
	return m.Update(cmd())
}

func TestExactNameSkipsNamePick(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog, the Infinite Gyre", Set: "uma",
		CollectorNumber: "7", Finishes: []string{"nonfoil", "foil"}}
	fs := fakeSearcher{
		prints: map[string][]scryfall.Card{"Ulamog, the Infinite Gyre": {card}},
	}
	m := newModel(context.Background(), fs, "Ulamog, the Infinite Gyre")

	// Init fires the prints search; deliver its message.
	mm, _ := m.Update(printsMsg{name: "Ulamog, the Infinite Gyre", cards: []scryfall.Card{card}})
	got := mm.(model)
	// Single printing → auto-skip print pick; two finishes → finish picker.
	if got.state != stateFinishPick {
		t.Fatalf("state = %v, want stateFinishPick", got.state)
	}
	if got.chosen == nil || got.chosen.ID != "u1" {
		t.Fatalf("chosen printing not set: %+v", got.chosen)
	}
}

func TestAmbiguousNameShowsNamePick(t *testing.T) {
	fs := fakeSearcher{
		auto: map[string][]string{"Ulamog": {"Ulamog, the Infinite Gyre", "Ulamog, the Ceaseless Hunger"}},
	}
	m := newModel(context.Background(), fs, "Ulamog")

	// Prints for "Ulamog" come back empty → triggers autocomplete.
	mm, cmd := m.Update(printsMsg{name: "Ulamog", cards: nil})
	// cmd is the autocomplete command; run it and feed the result.
	mm, _ = step(t, mm, cmd)
	got := mm.(model)
	if got.state != stateNamePick {
		t.Fatalf("state = %v, want stateNamePick", got.state)
	}
	if len(got.list.Items()) != 2 {
		t.Fatalf("name pick items = %d, want 2", len(got.list.Items()))
	}
}

func TestSinglePrintingSingleFinishSkipsToQty(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Foily", Set: "sld", CollectorNumber: "1",
		Finishes: []string{"foil"}} // one printing, one finish
	m := newModel(context.Background(), fakeSearcher{}, "Foily")

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty (both steps auto-skipped)", got.state)
	}
	if got.finish != "foil" {
		t.Errorf("finish = %q, want foil", got.finish)
	}
}

func TestNoMatchGoesToError(t *testing.T) {
	// Empty prints AND empty autocomplete → error state.
	m := newModel(context.Background(), fakeSearcher{}, "Zzz Nonexistent")
	mm, cmd := m.Update(printsMsg{name: "Zzz Nonexistent", cards: nil})
	mm, _ = step(t, mm, cmd) // run autocomplete → namesMsg{nil}
	got := mm.(model)
	if got.state != stateError || got.err == nil {
		t.Fatalf("state = %v err = %v, want stateError with an error", got.state, got.err)
	}
}

func TestConfirmAssemblesResult(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
	m := newModel(context.Background(), fakeSearcher{}, "Ulamog")
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("expected stateQty, got %v", got.state)
	}
	// Enter a quantity and submit.
	got.qtyInput.SetValue("3")
	mm, _ = got.submitQty()
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("expected stateConfirm, got %v", got.state)
	}
	// Confirm.
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)
	if got.result == nil {
		t.Fatal("result is nil after confirm")
	}
	if got.result.Qty != 3 || got.result.Finish != "normal" || got.result.Card.ID != "u1" {
		t.Errorf("result wrong: %+v", got.result)
	}
	if cmd == nil {
		t.Error("expected tea.Quit command on confirm")
	}
}

func TestQtyValidation(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, "x")
	m.state = stateQty
	m.qtyInput.SetValue("0")
	mm, _ := m.submitQty()
	got := mm.(model)
	if got.state != stateQty || got.qtyErr == "" {
		t.Errorf("qty 0 should stay on stateQty with an error, got state=%v err=%q", got.state, got.qtyErr)
	}
}

func TestFinishOptions(t *testing.T) {
	c := scryfall.Card{Finishes: []string{"foil", "nonfoil", "etched"}}
	got := finishOptions(c)
	want := []string{"normal", "foil", "etched"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("finishOptions = %v, want %v (stable order)", got, want)
	}
	if fo := finishOptions(scryfall.Card{}); len(fo) != 0 {
		t.Errorf("no finishes → empty, got %v", fo)
	}
}
