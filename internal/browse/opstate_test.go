package browse

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

// opModel builds a model with an injected update-prices op.
func opModel(t *testing.T, fn OpFunc) Model {
	t.Helper()
	m, err := New(testStore(), WithUpdatePrices(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)
	m.clock = func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) }
	return m
}

// runPaletteCommand runs a registry command by id through the palette-run
// path, returning the model and the command's Cmd.
func runPaletteCommand(t *testing.T, m Model, id string) (Model, tea.Cmd) {
	t.Helper()
	for i := range m.commands {
		if m.commands[i].id == id {
			cmd := m.commands[i].run(&m)
			return m, cmd
		}
	}
	t.Fatalf("no command %q", id)
	return m, nil
}

// The full lifecycle: start renders the spinner and badge, progress events
// update the status bar, done reports the summary and refreshes.
func TestOpLifecycle(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		return "prices updated · 2 printings", nil
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	if m.op == nil {
		t.Fatal("op did not start")
	}
	gen := m.op.gen

	// A determinate progress event lands in the status slot with a bar.
	next, _ := m.Update(opProgressMsg{gen: gen, ev: progress.Event{
		Step: "refreshing cards", Done: 1, Total: 2, Unit: progress.UnitCards}})
	m = next.(Model)
	line := m.statusLine()
	if !strings.Contains(line, "updating prices") || !strings.Contains(line, "1/2 cards") {
		t.Errorf("op status = %q", line)
	}
	if !strings.Contains(line, "█") || !strings.Contains(line, "░") {
		t.Errorf("op status has no bar: %q", line)
	}
	// The header badge keeps the op visible when a transient status covers
	// the line.
	m.status = "sorted by value"
	if out := m.View(); !strings.Contains(out, "updating prices") {
		t.Error("header badge missing while a transient status shows")
	}
	m.status = ""

	next, _ = m.Update(opDoneMsg{gen: gen, outcome: opOutcome{summary: "prices updated · 2 printings"}})
	m = next.(Model)
	if m.op != nil {
		t.Error("op still set after done")
	}
	if !strings.Contains(m.status, "prices updated · 2 printings · in ") {
		t.Errorf("completion status = %q", m.status)
	}
}

// esc cancels last in the chain; the cancelled outcome reports how far it
// had come.
func TestOpEscCancelsAndReportsProgress(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	gen := m.op.gen
	next, _ := m.Update(opProgressMsg{gen: gen, ev: progress.Event{
		Step: "refreshing cards", Done: 412, Total: 980, Unit: progress.UnitCards}})
	m = next.(Model)

	m = key(m, "esc")
	if !strings.Contains(m.status, "cancelling") {
		t.Errorf("status = %q after esc", m.status)
	}
	next, _ = m.Update(opDoneMsg{gen: gen, err: context.Canceled})
	m = next.(Model)
	if m.status != "cancelled · 412/980 cards done" {
		t.Errorf("cancelled status = %q", m.status)
	}
}

// One op at a time: a second start is refused with guidance, not queued.
func TestOpRefusesSecond(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	m, cmd := runPaletteCommand(t, m, "op.update-prices")
	if cmd != nil {
		t.Error("second start returned a command")
	}
	if !strings.Contains(m.status, "already running") {
		t.Errorf("status = %q, want the refusal", m.status)
	}
}

// Quitting mid-op stages a confirm whose yes cancels and quits; the add
// cascade is refused outright.
func TestOpGuardsQuitAndAdd(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "still running") {
		t.Fatalf("ctrl+c mid-op staged %+v, want the quit confirm", m.confirm)
	}
	m = key(m, "n") // anything but y cancels
	if m.confirm != nil {
		t.Error("confirm not cleared")
	}

	// Embedded, `a` no longer quits — without a constructor it refuses, and
	// the op keeps running untouched.
	m, cmd := runPaletteCommand(t, m, "add")
	if cmd != nil || m.addChild != nil {
		t.Error("add must not open or quit without a constructor")
	}
	if m.op == nil {
		t.Error("the running op must be untouched")
	}
	if !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q, want the unavailable refusal", m.status)
	}
}

// Messages from an abandoned run die on the generation check.
func TestOpStaleMessagesDropped(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	gen := m.op.gen

	next, _ := m.Update(opDoneMsg{gen: gen - 1, outcome: opOutcome{summary: "stale"}})
	m = next.(Model)
	if m.op == nil {
		t.Error("a stale done message ended the live op")
	}
	next, _ = m.Update(opProgressMsg{gen: gen - 1, ev: progress.Event{Done: 999, Total: 999}})
	m = next.(Model)
	if m.op.last.Done == 999 {
		t.Error("a stale progress message updated the live op")
	}
}

// The unavailable path: no injected op says so instead of doing nothing.
func TestOpUnavailable(t *testing.T) {
	m := newTestModel(t, testStore())
	m, cmd := runPaletteCommand(t, m, "undo") // sanity: registry lookup works on this model
	_ = cmd
	var st Model = m
	cmd = st.startOp("updating prices", nil)
	if cmd != nil || !strings.Contains(st.status, "unavailable") {
		t.Errorf("nil op: cmd=%v status=%q", cmd, st.status)
	}
}

// The status line's live slot replaces rather than appends: a pipeline of
// steps and notes reads one at a time after the title.
func TestOpStatusShowsNewestPieceOnly(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	gen := m.op.gen

	next, _ := m.Update(opProgressMsg{gen: gen, ev: progress.Event{Step: "downloading price history"}})
	m = next.(Model)
	if line := m.opStatus(); !strings.Contains(line, "downloading price history") {
		t.Fatalf("step missing: %q", line)
	}

	next, _ = m.Update(opProgressMsg{gen: gen, ev: progress.Event{
		Step: "downloading price history", Note: "fetching 90 days of prices for 14 printings"}})
	m = next.(Model)
	line := m.opStatus()
	if !strings.Contains(line, "fetching 90 days") {
		t.Fatalf("note missing: %q", line)
	}
	if strings.Contains(line, "downloading price history") {
		t.Fatalf("step must be replaced by the note, not stacked: %q", line)
	}
}

// The contradicted-price sweep is one paced vendor request per owned TCGplayer
// group — twenty seconds on a large hoard — and none of it is needed to put
// today's numbers on screen. Every price operation chains it as a follow-up,
// and the ordering assertion is the point: a sweep that ran inside the refresh
// would still finish, still refresh and still report, and would have changed
// nothing about the wait.
func TestPriceOpRunsTheCorrectionSweepAfterTheRefresh(t *testing.T) {
	var refreshDone, sweepStarted bool
	m, err := New(testStore(),
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			if sweepStarted {
				t.Error("the correction sweep started before the refresh finished")
			}
			refreshDone = true
			return "prices updated · 2 printings", nil
		}),
		WithCorrectPrices(func(ctx context.Context, p progress.Fn) (string, error) {
			if !refreshDone {
				t.Error("the correction sweep started before the refresh finished")
			}
			sweepStarted = true
			return "1 refused for sitting below the cheapest ask", nil
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)
	m.clock = func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) }

	m, cmd := runPaletteCommand(t, m, "op.update-prices")
	m = pump(t, m, cmd)

	if !sweepStarted {
		t.Fatal("the correction sweep never ran; the refresh dropped its follow-up")
	}
	if m.op != nil {
		t.Errorf("an op is still running after both halves: %q", m.op.title)
	}
	// Two operations, not one. This is the assertion that separates deferring
	// the sweep from merely calling it last: a sweep run inside the refresh
	// would set the flag above, produce the same summary and leave the same
	// panes — and would still have made the reader wait for it.
	if m.opGen != 2 {
		t.Errorf("%d operation(s) ran; want 2 — the refresh, then the sweep after it", m.opGen)
	}
	// The sweep's completion extends the refresh's line rather than replacing
	// it twenty seconds later with something narrower.
	for _, want := range []string{"prices updated · 2 printings", "1 refused"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("final status = %q, want it to carry %q", m.status, want)
		}
	}
}

// While the sweep runs the line still holds the reader's answer: statusLine
// puts a transient status above op progress precisely so a finished result is
// not shoved aside by the housekeeping that follows it, and the header badge is
// what says something is still going.
func TestDeferredSweepKeepsTheFinishedSummaryOnTheLine(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		return "prices updated · 2 printings", nil
	})
	m.opCorrectPrices = func(ctx context.Context, p progress.Fn) (string, error) {
		return "1 refused for sitting below the cheapest ask", nil
	}
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	gen := m.op.gen

	next, _ := m.Update(opDoneMsg{gen: gen, outcome: opOutcome{
		summary: "prices updated · 2 printings",
		then:    &followUp{title: "checking prices against asks", fn: m.opCorrectPrices},
	}})
	m = next.(Model)

	if m.op == nil {
		t.Fatal("the correction sweep did not start after the refresh")
	}
	if m.op.gen == gen {
		t.Error("the follow-up reused the refresh's generation; it is a separate operation")
	}
	if !strings.Contains(m.status, "prices updated · 2 printings") {
		t.Errorf("status = %q, want the refresh's summary kept while the sweep runs", m.status)
	}
	if out := m.View(); !strings.Contains(out, "checking prices against asks") {
		t.Error("no header badge for the running sweep")
	}
}

// Without an injected sweep the price ops behave exactly as they did: one
// operation, no follow-up, and refreshed prices under the corrections already
// in force.
func TestPriceOpWithoutACorrectionSweepChainsNothing(t *testing.T) {
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		return "prices updated · 2 printings", nil
	})
	m, _ = runPaletteCommand(t, m, "op.update-prices")
	gen := m.op.gen
	next, _ := m.Update(opDoneMsg{gen: gen, outcome: opOutcome{summary: "prices updated · 2 printings"}})
	m = next.(Model)
	if m.op != nil {
		t.Errorf("an op is running after done with no follow-up injected: %q", m.op.title)
	}
}
