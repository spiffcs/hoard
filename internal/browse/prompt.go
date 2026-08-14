package browse

import (
	tea "github.com/charmbracelet/bubbletea"
)

type prompt struct {
	label string
	text  string

	err string

	help string

	validate func(string) error

	commit func(*Model, string) tea.Cmd

	cancel func(*Model) tea.Cmd
}

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

func (m Model) promptLine() string {
	line := m.prompt.label + ": " + m.prompt.text + "▏"
	if m.prompt.err != "" {
		line += "  " + m.theme.Err.Render(m.prompt.err)
	}
	return line
}
