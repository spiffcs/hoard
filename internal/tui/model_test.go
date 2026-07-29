package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cphillips918/hoard/internal/scryfall"
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

// recordingAdder captures confirmed results and can be made to fail.
type recordingAdder struct {
	got []Result
	err error
}

func (r *recordingAdder) add(res Result) error {
	if r.err != nil {
		return r.err
	}
	r.got = append(r.got, res)
	return nil
}

func noopAdder(Result) error { return nil }

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

// isQuit reports whether running cmd yields a tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestExactNameSkipsNamePick(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog, the Infinite Gyre", Set: "uma",
		CollectorNumber: "7", Finishes: []string{"nonfoil", "foil"}}
	fs := fakeSearcher{
		prints: map[string][]scryfall.Card{"Ulamog, the Infinite Gyre": {card}},
	}
	m := newModel(context.Background(), fs, noopAdder, "Ulamog, the Infinite Gyre")

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
	m := newModel(context.Background(), fs, noopAdder, "Ulamog")

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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, "Foily")

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty (both steps auto-skipped)", got.state)
	}
	if got.finish != "foil" {
		t.Errorf("finish = %q, want foil", got.finish)
	}
}

func TestNoMatchKeepsSession(t *testing.T) {
	// Empty prints AND empty autocomplete → error banner, back on the name prompt.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, "Zzz Nonexistent")
	mm, cmd := m.Update(printsMsg{name: "Zzz Nonexistent", cards: nil})
	mm, _ = step(t, mm, cmd) // run autocomplete → namesMsg{nil}
	got := mm.(model)
	if got.state != stateName || !got.statusErr || got.status == "" {
		t.Fatalf("want stateName with an error banner, got state=%v statusErr=%v status=%q",
			got.state, got.statusErr, got.status)
	}
}

func TestConfirmAddsAndLoopsBack(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, "Ulamog")
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("expected stateQty, got %v", got.state)
	}
	// Enter a quantity and submit → confirm screen.
	got.qtyInput.SetValue("3")
	mm, _ = got.submitQty()
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("expected stateConfirm, got %v", got.state)
	}
	// Confirm.
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)

	// The adder received the right result.
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	r := ra.got[0]
	if r.Qty != 3 || r.Finish != "normal" || r.Card.ID != "u1" {
		t.Errorf("result wrong: %+v", r)
	}
	// Session loops back to the name prompt (not quit) with a success banner.
	if got.state != stateName {
		t.Errorf("state = %v, want stateName after confirm", got.state)
	}
	if got.addedCount != 1 || got.status == "" || got.statusErr {
		t.Errorf("session state wrong: count=%d status=%q err=%v", got.addedCount, got.status, got.statusErr)
	}
	if isQuit(cmd) {
		t.Error("confirm should NOT quit in add-mode")
	}
	if got.chosen != nil || got.nameInput.Value() != "" {
		t.Error("cascade selections/name not reset for next add")
	}
}

func TestAdderErrorKeepsSession(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}}
	ra := &recordingAdder{err: errors.New("disk full")}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, "Ulamog")
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model) // stateQty
	mm, _ = got.submitQty()
	got = mm.(model) // stateConfirm
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)

	if got.state != stateName || !got.statusErr {
		t.Fatalf("want stateName with error banner, got state=%v statusErr=%v", got.state, got.statusErr)
	}
	if got.addedCount != 0 {
		t.Errorf("addedCount = %d, want 0 on failed add", got.addedCount)
	}
	if isQuit(cmd) {
		t.Error("a failed add should not quit the session")
	}
}

func TestEscQuitsFromNameButCancelsMidCascade(t *testing.T) {
	card := scryfall.Card{ID: "a", Name: "A", Set: "x", CollectorNumber: "1", Finishes: []string{"nonfoil", "foil"}}
	// esc mid-cascade (print pick) → back to name prompt, not quit.
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"A": {card,
		{ID: "b", Name: "A", Set: "y", CollectorNumber: "2", Finishes: []string{"nonfoil"}}}}}
	m := newModel(context.Background(), fs, noopAdder, "A")
	mm, _ := m.Update(printsMsg{name: "A", cards: fs.prints["A"]})
	got := mm.(model)
	if got.state != statePrintPick {
		t.Fatalf("setup: want statePrintPick, got %v", got.state)
	}
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm2.(model)
	if got.state != stateName {
		t.Errorf("esc mid-cascade: state = %v, want stateName", got.state)
	}
	if isQuit(cmd) {
		t.Error("esc mid-cascade should not quit")
	}

	// esc at the name prompt → quit.
	_, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !isQuit(cmd) {
		t.Error("esc at name prompt should quit")
	}
}

func TestQtyValidation(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, "x")
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
