package browse

// textView is a full-screen scrollable text takeover: the valuation report,
// an import's outcome. Unlike the card overlay (detail.go) it renders lines
// it was handed rather than structured data, and it scrolls — a report is
// longer than a card.

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

// openText replaces the panes with a scrollable text takeover. The status
// line is left alone: the takeover doesn't render it, and an op summary set
// just before (onOpDone) must still be there when the takeover closes.
func (m *Model) openText(title string, lines []string) {
	// A text takeover replaces whatever full-screen surface was up; a card
	// detail left set would win the render and hide the report.
	m.detail = nil
	m.text = &textView{title: title, lines: lines}
}

// textRows is how many content lines fit; the same budget the panes get.
func (m Model) textRows() int {
	return m.visibleRows() + 1
}

// handleTextKey drives the takeover: scroll, close, or quit.
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
		// Same contract as the card overlay: the palette closes the takeover.
		m.text = nil
		m.openPalette()
	}
	return m, nil
}

// textViewRender renders the takeover in place of the panes, windowed by the
// scroll offset, with a footer saying where in the text the window sits.
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
	if len(t.lines) > rows {
		b.WriteString(m.theme.Help.Render(fmt.Sprintf("%s · lines %d–%d of %d",
			t.title, t.offset+1, min(t.offset+rows, len(t.lines)), len(t.lines))))
	} else {
		b.WriteString(m.theme.Help.Render(t.title))
	}
	b.WriteString("\n")
	m.writeHelp(&b, m.helpLine())
	return b.String()
}
