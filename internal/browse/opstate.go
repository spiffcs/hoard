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

// WithCorrectPrices supplies the pass that re-checks refreshed prices against
// the asks standing on their own listings, and records the observation.
//
// Injected apart from the refresh because it runs apart from it: it is the
// slowest thing a price update does — one paced vendor request per owned
// TCGplayer group, twenty seconds on a large hoard — and none of it is needed
// to put today's numbers on screen. Every operation that refreshes prices
// chains it as a follow-up, so it starts after the panes have already been
// redrawn with the result the reader was waiting for.
//
// Nil is a supported state: the follow-up simply does not run, and the browser
// shows refreshed prices under the corrections already in force.
func WithCorrectPrices(f OpFunc) Option { return func(m *Model) { m.opCorrectPrices = f } }

// WithRepairFinishes supplies the finish-repair operation.
func WithRepairFinishes(f OpFunc) Option { return func(m *Model) { m.opRepairFinishes = f } }

// WithCatalogUpdate supplies the catalog rebuild.
func WithCatalogUpdate(f OpFunc) Option { return func(m *Model) { m.opCatalogUpdate = f } }

// WithCatalogOffer(true) auto-starts the catalog download when the
// browser opens: no local catalog exists yet, and the best moment to
// fetch it is before the first add session needs its fast lookups — not
// after, when the slow lookups it would have prevented already happened.
// It runs as an ordinary operation — progress in the usual slot,
// cancellable like anything else — rather than behind a y/n: on a first
// run the download is the setup step, not a surprise.
func WithCatalogOffer(empty bool) Option { return func(m *Model) { m.catalogOffer = empty } }

// BackfillFunc imports days of price history from the MTGJSON archive —
// what makes an empty movers view populate. The archive holds ~90 days;
// days narrows what gets recorded.
type BackfillFunc func(ctx context.Context, p progress.Fn, days int) (summary string, err error)

// WithBackfill supplies the price-history import.
func WithBackfill(f BackfillFunc) Option { return func(m *Model) { m.opBackfill = f } }

// WatchAddFunc stands a watch by card name: the resolve-once pipeline needs
// the network, so it runs as an operation. Direction is explicit because no
// current price is known before the name resolves.
type WatchAddFunc func(ctx context.Context, p progress.Fn, name, op string, threshold float64) (string, error)

// WithWatchAddByName supplies name-based watch creation for the palette.
func WithWatchAddByName(f WatchAddFunc) Option { return func(m *Model) { m.opWatchAdd = f } }

// DeckAddFunc imports a deck from a pasted URL: the network acquisition and
// the resolve both belong inside the op, not the prompt commit.
type DeckAddFunc func(ctx context.Context, p progress.Fn, url string) (OpReport, error)

// WithDeckAddByURL supplies URL-based deck import for the palette.
func WithDeckAddByURL(f DeckAddFunc) Option { return func(m *Model) { m.opDeckAdd = f } }

// DeckAddFileFunc imports a deck from an exported decklist on disk — the
// escape hatch for the providers whose links cannot be fetched. Reading and
// resolving both happen inside the op; the deck's name comes from the file.
type DeckAddFileFunc func(ctx context.Context, p progress.Fn, path string) (OpReport, error)

// WithDeckAddByFile supplies file-based deck import for the palette.
func WithDeckAddByFile(f DeckAddFileFunc) Option { return func(m *Model) { m.opDeckAddFile = f } }

// ImportFunc imports a collection file: reading the file and resolving its
// rows both happen inside the op, off the UI thread. again acknowledges the
// ledger's already-imported refusal.
type ImportFunc func(ctx context.Context, p progress.Fn, path string, again bool) (OpReport, error)

// WithImportFile supplies file-based collection import for the palette.
func WithImportFile(f ImportFunc) Option { return func(m *Model) { m.opImport = f } }

// WatchImportFunc imports a watch-list file: reading and resolving happen
// inside the op. There is no again parameter — watch import upserts, so the
// ledger's already-imported question does not exist here.
type WatchImportFunc func(ctx context.Context, p progress.Fn, path string) (OpReport, error)

// WithWatchImportFile supplies file-based watch import for the palette.
func WithWatchImportFile(f WatchImportFunc) Option { return func(m *Model) { m.opWatchImport = f } }

// OpReport is a completed operation's outcome beyond the status line: an
// optional multi-line report for the text takeover, and import's
// already-imported refusal. It is the shape main's closures fill in;
// opOutcome is its browse-internal sibling that can also carry a staged
// follow-up confirm.
type OpReport struct {
	Summary string
	Report  []string
	// AlreadyImported carries the ledger refusal ("imported on DATE, N
	// cards"); the browser stages a confirm that re-runs with again=true.
	AlreadyImported string
}

// opOutcome is what a finished operation hands back. The confirm is built
// browse-side, in the wrapper closure where the op's arguments are still in
// scope — it rides the done message as data, never the progress channel
// (progress events are droppable narration; a confirm is load-bearing).
type opOutcome struct {
	summary string
	report  []string        // non-nil: opens the text takeover after the refresh
	confirm *pendingConfirm // staged last, e.g. import's run-again question
	// then is a second operation to start once this one's results are on
	// screen. Not a continuation — the panes are refreshed and the summary
	// posted in between, which is the entire point of separating them.
	then *followUp
}

// followUp is the deferred half of an operation: work that has to happen, does
// not have to happen first, and would otherwise be a long wait in front of a
// result the reader already has.
type followUp struct {
	title string
	fn    OpFunc
}

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
	outcome opOutcome
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
	return m.startOpReport(title, func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		summary, err := fn(ctx, p)
		return opOutcome{summary: summary}, err
	})
}

// startPriceOp is startOp for the operations that refresh prices, which all
// owe the same slow tail: re-checking the refreshed figures against the asks
// standing on their own listings, and recording the observation that follows
// from it.
//
// The tail rides as a follow-up rather than inside fn so it runs after the
// panes carry the new numbers. It is twenty seconds on a large hoard — one
// paced vendor request per owned TCGplayer group — and every second of it used
// to be spent in front of a result that was already computed.
func (m *Model) startPriceOp(title string, fn OpFunc) tea.Cmd {
	correct := m.opCorrectPrices
	if fn == nil || correct == nil {
		return m.startOp(title, fn)
	}
	return m.startOpReport(title, func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		summary, err := fn(ctx, p)
		if err != nil {
			return opOutcome{}, err
		}
		return opOutcome{summary: summary, then: &followUp{
			title: "checking prices against asks", fn: correct}}, nil
	})
}

// startFollowUp starts a deferred half without taking the status line.
//
// The line already holds the summary of the operation that just finished, and
// that summary is the reader's answer; the header badge is what says something
// is still running. statusLine's own precedence contract puts a transient
// status above op progress for exactly this reason, so the follow-up simply
// puts back what startOp cleared.
func (m *Model) startFollowUp(f *followUp) tea.Cmd {
	status, statusErr := m.status, m.statusErr
	cmd := m.startOp(f.title, f.fn)
	m.status, m.statusErr = status, statusErr
	return cmd
}

// startOpReport is startOp for operations whose outcome is richer than a
// status line — a report for the text takeover, a follow-up confirm.
func (m *Model) startOpReport(title string, fn func(context.Context, progress.Fn) (opOutcome, error)) tea.Cmd {
	if fn == nil {
		m.status, m.statusErr = title+" is unavailable in this build", true
		return nil
	}
	if m.op != nil {
		m.status, m.statusErr = m.op.title+" is already running · esc to cancel", true
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
		outcome, err := fn(ctx, mail.Fn())
		mail.Close()
		return opDoneMsg{gen: gen, outcome: outcome, err: err}
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
	m.status, m.statusErr = fmt.Sprintf("%s · in %s", msg.outcome.summary, took), false
	m.refresh()
	if err := m.loadView(); err != nil {
		m.setError(err)
		return m, nil
	}
	// A detail open through the whole run shows the refreshed numbers,
	// not the ones it was opened with. The op may have been a quote fetch,
	// so the comp memo is stale — clear it and let reloadDetail refetch.
	m.detailComps = nil
	compsCmd := m.reloadDetail()
	// Outcome extras, in order: the refresh above first (closing the
	// takeover must reveal current panes), then the report, then any
	// follow-up confirm on top.
	if msg.outcome.report != nil {
		m.openText(op.title, msg.outcome.report)
	}
	if msg.outcome.confirm != nil && m.confirm == nil {
		// The slot is virtually always free here; if a user confirm is
		// somehow up, dropping the follow-up is safe — the summary already
		// says what happened, and the action can be re-run.
		m.confirm = msg.outcome.confirm
	}
	// Last, after the refresh above and after the summary was posted: the
	// deferred half starts with the reader's result already on screen, which
	// is the only reason it was deferred. It carries the finished summary
	// forward so its own completion extends that line instead of replacing it
	// twenty seconds later with something narrower.
	if f := msg.outcome.then; f != nil && f.fn != nil {
		done := msg.outcome.summary
		tail := f.fn
		f = &followUp{title: f.title, fn: func(ctx context.Context, p progress.Fn) (string, error) {
			s, err := tail(ctx, p)
			switch {
			case err != nil:
				return "", err
			case s == "":
				return done, nil
			case done == "":
				return s, nil
			}
			return done + " · " + s, nil
		}}
		return m, tea.Batch(compsCmd, m.startFollowUp(f))
	}
	return m, compsCmd
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
	// One live slot after the title, showing the newest thing worth
	// showing: each event replaces the last rather than appending, so a
	// multi-step pipeline reads as a sequence instead of a run-on line
	// that outgrows the terminal.
	switch {
	case ev.Total > 0:
		frac := float64(ev.Done) / float64(ev.Total)
		s += " " + ui.ProgressBar(frac, opBarCells) + " " + ui.ProgressCounts(ev)
		if ev.Note != "" {
			s += " · " + ev.Note
		}
	case ev.Note != "":
		s += " · " + ev.Note
	case ev.Step != "":
		s += " · " + ev.Step
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
