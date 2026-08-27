package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func ulamog() scryfall.Card {
	return scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
}

func confirmOne(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{ulamog()}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty", got.state)
	}
	got.qtyInput.SetValue("1")
	mm, _ = submitQtyPastPaid(got)
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", got.state)
	}
	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	return mm.(model), cmd
}

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

func TestDecliningToLeaveCancelsThePendingQuit(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil,
		WithCompleter(func(Result) error { return nil }))

	got, _ := confirmOne(t, m)
	got, waited := leave(t, got)
	if isQuit(waited) {
		t.Fatal("the first leave should have waited")
	}

	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = mm.(model)
	if got.state != stateName {
		t.Fatalf("state = %v, want the session back at work after declining", got.state)
	}

	mm2, after := got.Update(completedMsg{card: ulamog()})
	if isQuit(after) {
		t.Error("the session quit even though the user declined to leave")
	}
	if mm2.(model).done {
		t.Error("the session marked itself done after the user declined to leave")
	}
}

func countingCompleter(seen *[]Result) Option {
	return WithCompleter(func(r Result) error {
		*seen = append(*seen, r)
		return nil
	})
}

func drainOne(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("the commit produced no command, so no completion was dispatched")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if inner, ok := c().(completedMsg); ok {
				mm, _ := m.Update(inner)
				return mm.(model)
			}
		}
		t.Fatal("no completion in the batch the commit returned")
	}
	if inner, ok := msg.(completedMsg); ok {
		mm, _ := m.Update(inner)
		return mm.(model)
	}
	t.Fatalf("commit returned %T, want a completion", msg)
	return m
}

func TestAutoCommittedScanIsCompleted(t *testing.T) {
	ev, fs := confidentFixture()
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil,
		WithCompleter(func(Result) error { return nil }))
	m, _ = openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if got.summary.Count("auto") != 1 {
		t.Fatalf("setup: summary = %+v, want one auto-add", got.summary.Entries)
	}
	if got.completing != 1 {
		t.Errorf("completing = %d after an auto-add; a scanned card would leave the "+
			"session with no document", got.completing)
	}
}

func TestPromotedSecondCopyIsCompleted(t *testing.T) {
	ev, fs := confidentFixture()
	var seen []Result
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil,
		countingCompleter(&seen))
	clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	first := ev
	first.FireReason = scan.FireRemoved
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
	got := resolve(t, mm.(model), first.CardList()[0])

	clock = clock.Add(900 * time.Millisecond)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: first})
	got = resolve(t, mm.(model), first.CardList()[0])
	if got.pending == nil {
		t.Fatal("setup: no held copy to promote")
	}
	before := got.completing

	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got = mm.(model)
	if got.summary.Count("duplicate-confirmed") != 1 {
		t.Fatalf("setup: summary = %+v, want the promotion recorded", got.summary.Entries)
	}
	if got.completing != before+1 {
		t.Errorf("completing = %d, was %d; the promoted copy was never completed",
			got.completing, before)
	}

	got = drainOne(t, got, cmd)
	if got.completing != before {
		t.Errorf("completing = %d after the promotion drained, want %d", got.completing, before)
	}
	if len(seen) != 1 || seen[0].Card.Name != "Sol Ring" {
		t.Errorf("completer saw %+v, want the promoted Sol Ring", seen)
	}
}

func TestCorrectedFinishIsCompleted(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Brainsurge": "Brainsurge"},
		prints: map[string][]scryfall.Card{"Brainsurge": {{ID: "mh3399",
			Name: "Brainsurge", Set: "mh3", CollectorNumber: "399",
			Finishes: []string{"nonfoil", "foil"}}}},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil,
		WithCompleter(func(Result) error { return nil }))
	clock := time.Date(2026, 8, 6, 23, 50, 58, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	blind := scan.Event{Kind: scan.EventScan, Name: "Brainsurge",
		Cards: []scan.Card{{Name: "Brainsurge", Candidates: []string{"Brainsurge"},
			Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: blind})
	m = resolve(t, mm.(model), blind.CardList()[0])
	before := m.completing

	clock = clock.Add(800 * time.Millisecond)
	marked := scan.Event{Kind: scan.EventScan, Name: "Brainsurge",
		FireReason: scan.FireMoved,
		Cards: []scan.Card{{Name: "Brainsurge", Candidates: []string{"Brainsurge"},
			FinishHint: "foil", Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: marked})
	m = resolve(t, mm.(model), marked.CardList()[0])

	if m.completing != before+1 {
		t.Errorf("completing = %d, was %d; the finish correction was never completed, "+
			"so the row keeps the wrong finish's prices", m.completing, before)
	}
}
