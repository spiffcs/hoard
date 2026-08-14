package browse

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

type OpFunc func(ctx context.Context, p progress.Fn) (summary string, err error)

func WithUpdatePrices(f OpFunc) Option { return func(m *Model) { m.opUpdatePrices = f } }

func WithCorrectPrices(f OpFunc) Option { return func(m *Model) { m.opCorrectPrices = f } }

func WithRepairFinishes(f OpFunc) Option { return func(m *Model) { m.opRepairFinishes = f } }

func WithCatalogUpdate(f OpFunc) Option { return func(m *Model) { m.opCatalogUpdate = f } }

func WithCatalogOffer(empty bool) Option { return func(m *Model) { m.catalogOffer = empty } }

type BackfillFunc func(ctx context.Context, p progress.Fn, days int) (summary string, err error)

func WithBackfill(f BackfillFunc) Option { return func(m *Model) { m.opBackfill = f } }

type WatchAddFunc func(ctx context.Context, p progress.Fn, name, op string, threshold float64) (string, error)

func WithWatchAddByName(f WatchAddFunc) Option { return func(m *Model) { m.opWatchAdd = f } }

type DeckAddFunc func(ctx context.Context, p progress.Fn, url string) (OpReport, error)

func WithDeckAddByURL(f DeckAddFunc) Option { return func(m *Model) { m.opDeckAdd = f } }

type DeckAddFileFunc func(ctx context.Context, p progress.Fn, path string) (OpReport, error)

func WithDeckAddByFile(f DeckAddFileFunc) Option { return func(m *Model) { m.opDeckAddFile = f } }

type ImportFunc func(ctx context.Context, p progress.Fn, path string, again bool) (OpReport, error)

func WithImportFile(f ImportFunc) Option { return func(m *Model) { m.opImport = f } }

type WatchImportFunc func(ctx context.Context, p progress.Fn, path string) (OpReport, error)

func WithWatchImportFile(f WatchImportFunc) Option { return func(m *Model) { m.opWatchImport = f } }

type OpReport struct {
	Summary string
	Report  []string

	AlreadyImported string
}

type opOutcome struct {
	summary string
	report  []string
	confirm *pendingConfirm

	then *followUp
}

type followUp struct {
	title string
	fn    OpFunc
}

type opState struct {
	title   string
	gen     int
	cancel  context.CancelFunc
	mail    *progress.Mailbox
	last    progress.Event
	started time.Time
}

type opProgressMsg struct {
	gen int
	ev  progress.Event
}
type opDoneMsg struct {
	gen     int
	outcome opOutcome
	err     error
}

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

func (m *Model) startFollowUp(f *followUp) tea.Cmd {
	status, statusErr := m.status, m.statusErr
	cmd := m.startOp(f.title, f.fn)
	m.status, m.statusErr = status, statusErr
	return cmd
}

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

func (m Model) onOpProgress(msg opProgressMsg) (tea.Model, tea.Cmd) {
	if m.op == nil || msg.gen != m.op.gen {
		return m, nil
	}
	m.op.last = msg.ev
	return m, awaitOpProgress(msg.gen, m.op.mail)
}

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

	m.detailComps = nil
	compsCmd := m.reloadDetail()

	if msg.outcome.report != nil {
		m.openText(op.title, msg.outcome.report)
	}
	if msg.outcome.confirm != nil && m.confirm == nil {

		m.confirm = msg.outcome.confirm
	}

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

func opProgressSoFar(ev progress.Event) string {
	if ev.Done <= 0 {
		return ""
	}
	return " · " + ui.ProgressCounts(ev) + " done"
}

func (m *Model) cancelOp() {
	if m.op == nil {
		return
	}
	m.op.cancel()
	m.status, m.statusErr = "cancelling "+m.op.title+"…", false
}

func (m Model) opStatus() string {
	ev := m.op.last
	s := m.spinner.View() + " " + m.op.title

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

const opBarCells = 12

func (m Model) opBadge() string {
	if m.op == nil {
		return ""
	}
	return " · " + m.spinner.View() + " " + m.op.title
}
