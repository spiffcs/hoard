package browse

// The op layer: long-running operations run in the background while the
// browser stays fully interactive. An op is background state, not an input
// mode — progress renders in the status-line slot and a header badge, and
// every key keeps working.
//
// The plumbing generalizes the arbitrage pattern: a generation counter so
// stale messages die, a per-op cancel, and one final message carrying the
// outcome. The addition is the progress pump: the operation's callback
// feeds a latest-value Mailbox, and a re-armed Cmd delivers each newest
// event into Update without the worker ever blocking on the UI.

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

// OpFunc is an injected long-running operation. It honours ctx, narrates
// through p, and returns its own one-line completion summary — the frontend
// that built the closure knows the result type; the browser only displays.
type OpFunc func(ctx context.Context, p progress.Fn) (summary string, err error)

// WithUpdatePrices supplies the price-refresh operation.
func WithUpdatePrices(f OpFunc) Option { return func(m *Model) { m.opUpdatePrices = f } }

// WithRepairFinishes supplies the finish-repair operation.
func WithRepairFinishes(f OpFunc) Option { return func(m *Model) { m.opRepairFinishes = f } }

// WithCatalogUpdate supplies the catalog rebuild.
func WithCatalogUpdate(f OpFunc) Option { return func(m *Model) { m.opCatalogUpdate = f } }

// WithBackfill supplies the 90-day price-history import — what makes an
// empty movers view populate.
func WithBackfill(f OpFunc) Option { return func(m *Model) { m.opBackfill = f } }

// WatchAddFunc stands a watch by card name: the resolve-once pipeline needs
// the network, so it runs as an operation. Direction is explicit because no
// current price is known before the name resolves.
type WatchAddFunc func(ctx context.Context, p progress.Fn, name, op string, threshold float64) (string, error)

// WithWatchAddByName supplies name-based watch creation for the palette.
func WithWatchAddByName(f WatchAddFunc) Option { return func(m *Model) { m.opWatchAdd = f } }

// opState is one operation in flight.
type opState struct {
	title   string
	gen     int
	cancel  context.CancelFunc
	mail    *progress.Mailbox
	last    progress.Event
	started time.Time
}

// opProgressMsg carries the newest progress event; opDoneMsg the outcome.
// Both carry the generation so anything from an abandoned run is dropped.
type opProgressMsg struct {
	gen int
	ev  progress.Event
}
type opDoneMsg struct {
	gen     int
	summary string
	err     error
}

// startOp launches fn in the background, refusing while another op runs —
// a queue would be hidden state with no UI to inspect it, and every op here
// is cheap to re-request.
func (m *Model) startOp(title string, fn OpFunc) tea.Cmd {
	if fn == nil {
		m.status, m.statusErr = title+" is unavailable in this build", true
		return nil
	}
	if m.op != nil {
		m.status, m.statusErr = m.op.title+" is already running — esc to cancel", true
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.opGen++
	mail := progress.NewMailbox()
	m.op = &opState{
		title: title, gen: m.opGen, cancel: cancel, mail: mail, started: m.now(),
	}
	m.status, m.statusErr = "", false

	gen := m.opGen
	run := func() tea.Msg {
		summary, err := fn(ctx, mail.Fn())
		mail.Close()
		return opDoneMsg{gen: gen, summary: summary, err: err}
	}
	return tea.Batch(m.spinner.Tick, run, awaitOpProgress(gen, mail))
}

// awaitOpProgress yields the next progress event; Update re-arms it after
// each delivery. The Done branch releases the pump when the op ends between
// events, so no goroutine outlives its operation.
func awaitOpProgress(gen int, mail *progress.Mailbox) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-mail.C():
			return opProgressMsg{gen: gen, ev: ev}
		case <-mail.Done():
			return nil
		}
	}
}

// onOpProgress stores the newest event and re-arms the pump.
func (m Model) onOpProgress(msg opProgressMsg) (tea.Model, tea.Cmd) {
	if m.op == nil || msg.gen != m.op.gen {
		return m, nil
	}
	m.op.last = msg.ev
	return m, awaitOpProgress(msg.gen, m.op.mail)
}

// onOpDone ends the operation: summary or error to the status line, then a
// cursor-preserving refresh so the pane the op most likely changed is
// current.
func (m Model) onOpDone(msg opDoneMsg) (tea.Model, tea.Cmd) {
	if m.op == nil || msg.gen != m.op.gen {
		return m, nil
	}
	op := m.op
	m.op = nil
	op.cancel()

	if msg.err != nil {
		if isCanceled(msg.err) {
			m.status, m.statusErr = "cancelled"+opProgressSoFar(op.last), false
			return m, nil
		}
		m.setError(msg.err)
		return m, nil
	}

	took := m.now().Sub(op.started).Round(time.Second)
	m.status, m.statusErr = fmt.Sprintf("%s · in %s", msg.summary, took), false
	m.refresh()
	if err := m.loadValueSeries(); err != nil {
		m.setError(err)
		return m, nil
	}
	if err := m.loadView(); err != nil {
		m.setError(err)
	}
	return m, nil
}

// opProgressSoFar says how far a cancelled op had come, when its last event
// carried counts.
func opProgressSoFar(ev progress.Event) string {
	if ev.Done <= 0 {
		return ""
	}
	return " · " + ui.ProgressCounts(ev) + " done"
}

// cancelOp asks the running op to stop; the outcome arrives as its
// opDoneMsg.
func (m *Model) cancelOp() {
	if m.op == nil {
		return
	}
	m.op.cancel()
	m.status, m.statusErr = "cancelling "+m.op.title+"…", false
}

// opStatus is the status-line slot while an op runs: spinner, title, a bar
// when the newest event is determinate, the step or note otherwise.
func (m Model) opStatus() string {
	ev := m.op.last
	s := m.spinner.View() + " " + m.op.title
	if ev.Total > 0 {
		frac := float64(ev.Done) / float64(ev.Total)
		s += " " + ui.ProgressBar(frac, opBarCells) + " " + ui.ProgressCounts(ev)
	} else if ev.Step != "" {
		s += " · " + ev.Step
	}
	if ev.Note != "" {
		s += " · " + ev.Note
	}
	return s
}

// opBarCells matches the CLI printer's bar width; the line also carries the
// title and counts.
const opBarCells = 12

// opBadge is the always-visible presence marker in the header, so a
// transient status covering the line never makes a minutes-long op look
// dead.
func (m Model) opBadge() string {
	if m.op == nil {
		return ""
	}
	return " · " + m.spinner.View() + " " + m.op.title
}
