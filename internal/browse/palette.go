package browse

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

func commandRank(m *Model, c *command) int {
	if c.rank == nil {
		return 0
	}
	return c.rank(m)
}

type palette struct {
	ui.Palette
	items   []ui.PaletteItem
	backing []int
	matches []paletteMatch
}

type paletteMatch struct {
	index     int
	positions []int
}

func (m *Model) openPalette() {
	m.palette = &palette{}
	m.status = ""
	m.refreshPalette()
}

var detailPaletteIDs = map[string]bool{
	"op.update-prices":   true,
	"op.backfill":        true,
	"op.backfill.90":     true,
	"op.repair-finishes": true,
	"market.fetch":       true,
	"op.cancel":          true,
}

func (m *Model) refreshPalette() {
	p := m.palette
	p.items = p.items[:0]
	p.backing = p.backing[:0]

	for i := range m.commands {

		if m.commands[i].hidden || !m.commands[i].applies(m) {
			continue
		}
		if h := m.commands[i].hide; h != nil && h(m) {
			continue
		}

		if m.detail != nil && !detailPaletteIDs[m.commands[i].id] {
			continue
		}
		p.items = append(p.items, ui.PaletteItem{
			Title:   m.commands[i].title,
			Aliases: m.commands[i].aliases,
			Desc:    m.commands[i].desc,
			Key:     m.commands[i].key,
			Rank:    commandRank(m, &m.commands[i]),
		})
		p.backing = append(p.backing, i)
	}
	p.Refresh(p.items)

	p.matches = p.matches[:0]
	for _, pm := range p.Matches() {
		p.matches = append(p.matches, paletteMatch{
			index: p.backing[pm.Index], positions: pm.Positions})
	}
}

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
		c := m.commands[p.matches[p.Cursor].index]

		m.palette = nil
		if !c.applies(&m) {
			return m, nil
		}
		return m, c.run(&m)
	case tea.KeyUp:
		p.Up()
		return m, nil
	case tea.KeyDown:
		p.Down()
		return m, nil
	case tea.KeyBackspace:
		p.Backspace()
		m.refreshPalette()
		return m, nil
	case tea.KeyCtrlU:
		p.Clear()
		m.refreshPalette()
		return m, nil
	case tea.KeySpace:
		p.Type(" ")
		m.refreshPalette()
		return m, nil
	case tea.KeyRunes:
		p.Type(string(msg.Runes))
		m.refreshPalette()
		return m, nil
	}
	return m, nil
}

func (m Model) paletteRows() int {
	if m.palette == nil {
		return 0
	}
	return m.palette.Rows()
}

func (m Model) paletteLines(width int) []string {
	return m.palette.Lines(m.palette.items, width, m.theme)
}

func (m Model) paletteDesc() string {
	if m.palette == nil {
		return ""
	}
	return m.palette.Desc(m.palette.items)
}
