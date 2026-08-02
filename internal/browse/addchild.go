package browse

// The embedded add cascade. Pressing `a` used to quit the browser with a
// flag so the caller could run the cascade as its own program and re-enter
// — two programs taking turns with the terminal. Embedding runs the same
// cascade as a child model instead: the browser keeps the terminal, ops
// keep running behind the takeover, and cursor/filter/undo state survives
// an add. The cascade's own state machine is untouched (internal/tui); the
// browser only routes messages, sizes it, and owns the two things a
// standalone program got for free — teardown of the camera session on
// quit, and the exit receipt.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/tui"
)

// WithAddCascade supplies the add-cascade constructor. The browser never
// builds the cascade's dependencies (catalog searcher, store adder,
// platform scanner) itself — main hands over a factory, one fresh cascade
// per invocation, so destinations re-read and no cascade state leaks
// between adds. Without it, `a` reports the capability missing.
func WithAddCascade(newChild func() (tui.Child, error)) Option {
	return func(m *Model) { m.newAddChild = newChild }
}

// openAddCascade starts a fresh cascade as the active takeover.
func (m *Model) openAddCascade() tea.Cmd {
	if m.newAddChild == nil {
		m.status, m.statusErr = "adding is unavailable in this build", true
		return nil
	}
	child, err := m.newAddChild()
	if err != nil {
		m.status, m.statusErr = "add unavailable: "+err.Error(), true
		return nil
	}
	// Size it before its first render — the cascade must never draw its
	// default geometry inside a frame that already knows better.
	child, _ = child.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.addChild = &child
	m.status = ""
	return child.Init()
}

// forwardToChild is the single funnel every message takes into the cascade,
// and the one place its completion is noticed.
func (m Model) forwardToChild(msg tea.Msg) (tea.Model, tea.Cmd) {
	child, cmd := m.addChild.Update(msg)
	m.addChild = &child
	if child.Done() {
		return m.closeAddChild()
	}
	return m, cmd
}

// closeAddChild retires a finished cascade: fold its receipt into the
// session summary, put the one-line outcome on the status line, and re-read
// what the adds changed. The re-read happens here, once, rather than per
// card — the panes are invisible behind the takeover, so mid-cascade
// refreshes would be work nobody can see.
func (m Model) closeAddChild() (tea.Model, tea.Cmd) {
	child := m.addChild
	m.addChild = nil
	m.addSummary.Entries = append(m.addSummary.Entries, child.Summary().Entries...)
	if err := child.Err(); err != nil {
		m.status, m.statusErr = "add ended: "+err.Error(), true
	} else {
		m.status, m.statusErr = addReceiptLine(child.Added(), child.AddedValue(), child.Summary()), false
	}
	m.refresh()
	if err := m.loadView(); err != nil {
		m.setError(err)
	}
	return m, nil
}

// teardownAddChild force-ends a live cascade because the browser itself is
// quitting: the camera session must die before the program does, and
// whatever the cascade abandoned goes into the receipt as discarded.
func (m *Model) teardownAddChild() {
	if m.addChild == nil {
		return
	}
	m.addChild.Close()
	m.addSummary.Entries = append(m.addSummary.Entries, m.addChild.Summary().Entries...)
	m.addChild = nil
}

// handleAddChildKey routes keys while the cascade owns the screen. Exactly
// one key belongs to the browser: ctrl+c is the app-wide hard exit
// everywhere else, and it would be the one surface where it means something
// softer if the cascade kept it. Everything else — including esc, which
// walks the cascade's own back-out chain and its close-prompt gate on
// queued cards — is the cascade's.
func (m Model) handleAddChildKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if m.op != nil {
			title := m.op.title
			m.confirm = &pendingConfirm{
				prompt: title + " is still running. Quit anyway?",
				help:   "y quit · any other key stays",
				onYes: func(m *Model) tea.Cmd {
					m.teardownAddChild()
					m.cancelOp()
					return tea.Quit
				},
			}
			return m, nil
		}
		m.teardownAddChild()
		return m, tea.Quit
	}
	return m.forwardToChild(msg)
}

// addReceiptLine is one cascade invocation's outcome as a status line.
// added counts every commit (manual adds record no summary entry — the
// summary is the scan receipt), value is their qty-weighted worth, and the
// breakdown names the scan-origin kinds when there are any.
func addReceiptLine(added int, value float64, s tui.Summary) string {
	if added == 0 {
		return "no cards added"
	}
	line := fmt.Sprintf("added %d", added)
	if value > 0 {
		line += fmt.Sprintf(" ($%.2f)", value)
	}
	var parts []string
	if n := s.Count("auto"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d auto", n))
	}
	if n := s.Count("reviewed"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d reviewed", n))
	}
	if n := s.Count("duplicate-confirmed"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d confirmed duplicate", n))
	}
	if len(parts) > 0 {
		line += " · " + strings.Join(parts, ", ")
	}
	return line
}
