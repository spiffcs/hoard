package browse

// The inline prompt: a one-line input living in the status-line slot, the
// filter bar's shape generalized. Palette actions with arguments, watch
// thresholds, and binder names all ask through this rather than each
// growing a third copy of the rune-accumulation code.
//
// Deliberately single-field and short: no cursor movement, no multi-line.
// A commit may set the next prompt to chain steps, but every step stays one
// small answer.

import (
	tea "github.com/charmbracelet/bubbletea"
)

type prompt struct {
	// label introduces the input: "new binder name". Rendered as
	// "label: text▏".
	label string
	text  string
	// err is the live validation failure, shown after the caret; it clears
	// on the next keystroke.
	err string
	// help, when set, replaces the generic prompt help line — for inputs
	// with their own syntax worth spelling out (watch thresholds).
	help string
	// validate, when set, runs on enter; a non-nil error refuses the commit
	// and shows instead.
	validate func(string) error
	// commit runs on an accepted enter, after the prompt closes — it may
	// open the next prompt or a confirm.
	commit func(*Model, string) tea.Cmd
	// cancel, when set, runs after esc closes the prompt — for a prompt
	// opened by a flow that must put the reader back where it began.
	cancel func(*Model) tea.Cmd
}

// handlePromptKey edits the pending answer.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		p := m.prompt
		m.prompt = nil
		m.status, m.statusErr = "cancelled", false
		if p.cancel != nil {
			return m, p.cancel(&m)
		}
		return m, nil
	case tea.KeyEnter:
		p := m.prompt
		if p.validate != nil {
			if err := p.validate(p.text); err != nil {
				m.prompt.err = err.Error()
				return m, nil
			}
		}
		m.prompt = nil
		if p.commit == nil {
			return m, nil
		}
		return m, p.commit(&m, p.text)
	case tea.KeyBackspace:
		if m.prompt.text != "" {
			r := []rune(m.prompt.text)
			m.prompt.text = string(r[:len(r)-1])
		}
		m.prompt.err = ""
		return m, nil
	case tea.KeyCtrlU:
		m.prompt.text, m.prompt.err = "", ""
		return m, nil
	case tea.KeySpace:
		m.prompt.text += " "
		m.prompt.err = ""
		return m, nil
	case tea.KeyRunes:
		m.prompt.text += string(msg.Runes)
		m.prompt.err = ""
		return m, nil
	}
	return m, nil
}

// promptLine renders the prompt into the status-line slot.
func (m Model) promptLine() string {
	line := m.prompt.label + ": " + m.prompt.text + "▏"
	if m.prompt.err != "" {
		line += "  " + errStyle.Render(m.prompt.err)
	}
	return line
}
