package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func ulamog() scryfall.Card {
	return scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
}

// Walks the model to a committed add, returning the model and the command the
// confirm produced.
func confirmOne(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{ulamog()}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty", got.state)
	}
	got.qtyInput.SetValue("1")
	mm, _ = got.submitQty()
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", got.state)
	}
	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	return mm.(model), cmd
}

// Asks to leave and answers yes.
func leave(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	m.leaveFrom = stateName
	m.state = stateLeaveConfirm
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	return mm.(model), cmd
}

func TestConfirmDoesNotBlockOnTheCompleter(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(Result) error {
			<-release
			return nil
		}))

	go func() {
		defer close(done)
		confirmOne(t, m)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("confirm blocked on the completer; the network must not run on the update loop")
	}
	close(release)
}

func TestLeavingWaitsForOutstandingCompletions(t *testing.T) {
	var asked []Result
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(r Result) error {
			asked = append(asked, r)
			return nil
		}))

	got, cmd := confirmOne(t, m)
	if cmd == nil {
		t.Fatal("confirm produced no command; the completion was never dispatched")
	}
	if got.completing != 1 {
		t.Fatalf("completing = %d, want the one card in flight", got.completing)
	}

	got, quit := leave(t, got)
	if isQuit(quit) {
		t.Error("the session quit while a card was still being completed")
	}
	if got.done {
		t.Error("the session reported itself done with a completion outstanding")
	}

	got2, after := got.Update(completedMsg{card: ulamog()})
	got = got2.(model)
	if got.completing != 0 {
		t.Errorf("completing = %d after the last completion landed", got.completing)
	}
	if !isQuit(after) {
		t.Error("the session did not quit once the last completion landed")
	}
	if !got.done {
		t.Error("the session is not marked done after draining")
	}
}

func TestLeavingQuitsAtOnceWithNothingOutstanding(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil)
	_, quit := leave(t, m)
	if !isQuit(quit) {
		t.Error("a session with nothing in flight should quit immediately")
	}
}

func TestASecondQuitAbandonsOutstandingCompletions(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(Result) error { return nil }))

	got, _ := confirmOne(t, m)
	got, first := leave(t, got)
	if isQuit(first) {
		t.Fatal("the first leave should have waited")
	}

	got2, second := got.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuit(second) {
		t.Error("ctrl-c while draining should abandon the wait and quit")
	}
	if !got2.(model).done {
		t.Error("abandoning did not mark the session done")
	}
}

func TestACompletionFailureIsNotedAndStillDrains(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(Result) error { return errors.New("scryfall is down") }))

	got, _ := confirmOne(t, m)
	got, _ = leave(t, got)

	got2, after := got.Update(completedMsg{card: ulamog(), err: errors.New("scryfall is down")})
	got = got2.(model)
	if got.completing != 0 {
		t.Errorf("completing = %d; a failure must still clear the slot", got.completing)
	}
	if !isQuit(after) {
		t.Error("a failed completion left the session hanging")
	}
	if got.summary.Count("incomplete") != 1 {
		t.Errorf("summary = %+v, want the card recorded as incomplete", got.summary.Entries)
	}
}

func TestChildStaysUndoneWhileCompletionsDrain(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(Result) error { return nil }))

	c.m, _ = confirmOne(t, c.m)
	c.m, _ = leave(t, c.m)
	if c.Done() {
		t.Fatal("the embedded session reported done with a completion outstanding; " +
			"browse would tear it down and drop the work")
	}

	next, _ := c.Update(completedMsg{card: ulamog()})
	if !next.Done() {
		t.Error("the embedded session never finished after its completion landed")
	}
}
