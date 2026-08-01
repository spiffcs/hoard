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

	next, _ = m.Update(opDoneMsg{gen: gen, summary: "prices updated · 2 printings"})
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

	m = key(m, "q")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "still running") {
		t.Fatalf("q mid-op staged %+v, want the quit confirm", m.confirm)
	}
	m = key(m, "n") // anything but y cancels
	if m.confirm != nil {
		t.Error("confirm not cleared")
	}

	m, cmd := runPaletteCommand(t, m, "add")
	if cmd != nil || m.wantAdd {
		t.Error("add cascade started mid-op")
	}
	if !strings.Contains(m.status, "wait for") {
		t.Errorf("status = %q, want the add refusal", m.status)
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

	next, _ := m.Update(opDoneMsg{gen: gen - 1, summary: "stale"})
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
