package browse

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/tui"
)

func WithAddCascade(newChild func() (tui.Child, error)) Option {
	return func(m *Model) { m.newAddChild = newChild }
}

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

	child.Restore(m.addPending)
	m.addPending = tui.Pending{}

	child, _ = child.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.addChild = &child
	m.status = ""
	return child.Init()
}

func (m Model) forwardToChild(msg tea.Msg) (tea.Model, tea.Cmd) {
	child, cmd := m.addChild.Update(msg)
	m.addChild = &child
	if child.Done() {
		return m.closeAddChild()
	}
	return m, cmd
}

func (m Model) closeAddChild() (tea.Model, tea.Cmd) {
	child := m.addChild
	m.addChild = nil

	m.addPending = child.Pending()
	m.addSummary.Entries = append(m.addSummary.Entries, child.Summary().Entries...)
	if err := child.Err(); err != nil {
		m.status, m.statusErr = "add ended: "+err.Error(), true
	} else {
		m.status, m.statusErr = addReceiptLine(child.Added(), child.AddedValue(), child.Summary()), false

		if n := m.addPending.Len(); n > 0 {
			m.status += fmt.Sprintf(" · %d still waiting for review (press a to finish them)", n)
		}
	}
	m.refresh()
	if err := m.loadView(); err != nil {
		m.setError(err)
	}

	if child.Added() > 0 && m.op == nil && m.opUpdatePrices != nil {
		return m, m.populateView()
	}
	return m, nil
}

func (m *Model) teardownAddChild() {

	if n := m.addPending.Len(); n > 0 {
		m.addSummary.Entries = append(m.addSummary.Entries, tui.SummaryEntry{
			Kind: "discarded",
			Line: fmt.Sprintf("%d scanned cards discarded unprocessed", n),
		})
		m.addPending = tui.Pending{}
	}
	if m.addChild == nil {
		return
	}
	m.addChild.Close()
	m.addSummary.Entries = append(m.addSummary.Entries, m.addChild.Summary().Entries...)
	m.addChild = nil
}

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
