package browse

// The command palette: every registry command, fuzzy-searchable, in a
// bottom-anchored drawer. It is the browser's exhaustive reference — the
// help line stays curated prose, but ':' always shows everything that
// applies right now.
//
// A full-width drawer rather than a floating box: the no-floating-box rule
// exists because partial-width boxes leave table fragments visible around
// their edges, and a horizontal split has no edges to leak around — it is
// the filter bar's geometry, grown upward. A full-screen takeover would
// hide the very rows that give contextual commands their subject.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/spiffcs/hoard/internal/ui"
)

// paletteMaxRows is the most matches the drawer shows at once; the query
// narrows the rest into view.
const paletteMaxRows = 8

type palette struct {
	query   string
	cursor  int
	matches []paletteMatch
}

// paletteMatch is one applicable command, with the rune positions the query
// matched (title positions only) for bolding.
type paletteMatch struct {
	index     int // into m.commands
	positions []int
}

// openPalette opens the drawer over the current view with everything
// applicable listed.
func (m *Model) openPalette() {
	m.palette = &palette{}
	m.status = ""
	m.refreshPalette()
}

// refreshPalette recomputes the matches for the current query. Empty query:
// every applicable command in registry order. Otherwise: fuzzy over
// title+aliases, ranked by match quality.
func (m *Model) refreshPalette() {
	p := m.palette
	p.matches = p.matches[:0]

	applicable := make([]int, 0, len(m.commands))
	targets := make([]string, 0, len(m.commands))
	for i := range m.commands {
		if !m.commands[i].applies(m) {
			continue
		}
		applicable = append(applicable, i)
		targets = append(targets, m.commands[i].title+" "+m.commands[i].aliases)
	}

	if p.query == "" {
		for _, idx := range applicable {
			p.matches = append(p.matches, paletteMatch{index: idx})
		}
	} else {
		for _, fm := range fuzzy.Find(p.query, targets) {
			idx := applicable[fm.Index]
			titleLen := len([]rune(m.commands[idx].title))
			var pos []int
			for _, at := range fm.MatchedIndexes {
				if at < titleLen {
					pos = append(pos, at)
				}
			}
			p.matches = append(p.matches, paletteMatch{index: idx, positions: pos})
		}
	}
	p.cursor = min(p.cursor, max(len(p.matches)-1, 0))
}

// handlePaletteKey drives the drawer.
func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.palette
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.palette = nil
		return m, nil
	case tea.KeyEnter:
		if len(p.matches) == 0 {
			return m, nil
		}
		c := m.commands[p.matches[p.cursor].index]
		// Close first, then run: the command may open a prompt or a
		// confirm, which owns the same screen space the drawer held.
		m.palette = nil
		if !c.applies(&m) {
			return m, nil
		}
		return m, c.run(&m)
	case tea.KeyUp:
		p.cursor = max(p.cursor-1, 0)
		return m, nil
	case tea.KeyDown:
		p.cursor = min(p.cursor+1, max(len(p.matches)-1, 0))
		return m, nil
	case tea.KeyBackspace:
		if p.query != "" {
			r := []rune(p.query)
			p.query = string(r[:len(r)-1])
		}
		m.refreshPalette()
		return m, nil
	case tea.KeyCtrlU:
		p.query = ""
		m.refreshPalette()
		return m, nil
	case tea.KeySpace:
		p.query += " "
		m.refreshPalette()
		return m, nil
	case tea.KeyRunes:
		p.query += string(msg.Runes)
		m.refreshPalette()
		return m, nil
	}
	return m, nil
}

// paletteRows is how many drawer rows the palette currently costs the
// panes; zero when closed. At least one row renders even with no match, so
// "no matching command" has somewhere to say itself.
func (m Model) paletteRows() int {
	if m.palette == nil {
		return 0
	}
	return max(min(len(m.palette.matches), paletteMaxRows), 1)
}

// paletteLines renders the drawer's match rows at the given width.
func (m Model) paletteLines(width int) []string {
	p := m.palette
	if len(p.matches) == 0 {
		return []string{helpStyle.Render("no matching command")}
	}

	// The window follows the cursor, same as the panes.
	shown := m.paletteRows()
	start := 0
	if p.cursor >= shown {
		start = p.cursor - shown + 1
	}

	lines := make([]string, 0, shown)
	for i := start; i < min(start+shown, len(p.matches)); i++ {
		match := p.matches[i]
		c := m.commands[match.index]

		marker := "  "
		if i == p.cursor {
			marker = "▸ "
		}
		title := boldRunes(c.title, match.positions)
		hint := c.key

		// Title left, key hint right-aligned dim; measured on the plain
		// text so the bolding never shifts the column.
		plainWidth := 2 + len([]rune(c.title))
		gap := max(width-plainWidth-len([]rune(hint))-1, 1)
		line := marker + title + strings.Repeat(" ", gap) + helpStyle.Render(hint)
		if i == p.cursor {
			line = cursorStyle.Render(marker+c.title) + strings.Repeat(" ", gap) + helpStyle.Render(hint)
		}
		lines = append(lines, ui.Truncate(line, width))
	}
	return lines
}

// boldRunes bolds the matched rune positions of s — the palette's only
// styling, per the bold/dim-only rule.
func boldRunes(s string, positions []int) string {
	if len(positions) == 0 {
		return s
	}
	set := make(map[int]bool, len(positions))
	for _, p := range positions {
		set[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if set[i] {
			b.WriteString(titleStyle.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
