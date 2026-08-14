package browse

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type textView struct {
	title  string
	lines  []string
	offset int
}

func (m *Model) openText(title string, lines []string) {

	m.detail = nil
	m.text = &textView{title: title, lines: lines}
}

func (m Model) textRows() int {
	return m.visibleRows() + 1
}

func (m Model) handleTextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.text
	maxOffset := max(len(t.lines)-m.textRows(), 0)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "enter", "backspace":
		m.text = nil
	case "up", "k":
		t.offset = max(t.offset-1, 0)
	case "down", "j":
		t.offset = min(t.offset+1, maxOffset)
	case "pgup":
		t.offset = max(t.offset-m.textRows(), 0)
	case "pgdown", " ":
		t.offset = min(t.offset+m.textRows(), maxOffset)
	case "g", "home":
		t.offset = 0
	case "G", "end":
		t.offset = maxOffset
	case ":", "ctrl+p":

		m.text = nil
		m.openPalette()
	}
	return m, nil
}

func (m Model) textViewRender() string {
	t := m.text
	rows := m.textRows()

	var b strings.Builder
	for i := range rows {
		idx := t.offset + i
		line := ""
		if idx < len(t.lines) {
			line = t.lines[idx]
		}
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")

	switch {
	case m.confirm != nil || m.prompt != nil:
		b.WriteString(m.statusLine())
	case len(t.lines) > rows:
		b.WriteString(m.theme.Help.Render(fmt.Sprintf("%s · lines %d–%d of %d",
			t.title, t.offset+1, min(t.offset+rows, len(t.lines)), len(t.lines))))
	default:
		b.WriteString(m.theme.Help.Render(t.title))
	}
	b.WriteString("\n")
	m.writeHelp(&b, m.helpLine())
	return b.String()
}
