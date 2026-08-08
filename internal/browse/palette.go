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
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

// commandRank resolves a command's ordering weight for the current state.
func commandRank(m *Model, c *command) int {
	if c.rank == nil {
		return 0
	}
	return c.rank(m)
}

// palette is the drawer's state plus the mapping back into the registry.
// ui.Palette matches over a flat item list and knows nothing about which
// commands apply; `backing` carries each item's index in m.commands so a
// match can be run, and `matches` is that mapping already applied — the
// shape the rest of this package and its tests reason in.
type palette struct {
	ui.Palette
	items   []ui.PaletteItem
	backing []int
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

// detailPaletteIDs are the commands the palette offers over the card
// detail: the ones that refresh price data, and the cancel for a refresh
// already running.
var detailPaletteIDs = map[string]bool{
	"op.update-prices":   true,
	"op.backfill":        true,
	"op.backfill.90":     true,
	"op.repair-finishes": true,
	"market.fetch":       true,
	"op.cancel":          true,
}

// refreshPalette rebuilds the applicable set and re-runs the match. Which
// commands apply is this package's question; the matching is not.
func (m *Model) refreshPalette() {
	p := m.palette
	p.items = p.items[:0]
	p.backing = p.backing[:0]

	for i := range m.commands {
		// Hidden commands keep their keys but never list: the palette is
		// for the verbs, not the navigation reflexes. hide is the same,
		// contextually.
		if m.commands[i].hidden || !m.commands[i].applies(m) {
			continue
		}
		if h := m.commands[i].hide; h != nil && h(m) {
			continue
		}
		// Over the card detail the palette narrows to the price refreshers
		// (and cancelling one): the overlay is a reading surface, and the
		// one thing worth running from it is a refresh of the numbers on
		// it. Everything else waits an esc away.
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

	// Back into registry indices, so nothing downstream carries two
	// numbering schemes for the same row.
	p.matches = p.matches[:0]
	for _, pm := range p.Matches() {
		p.matches = append(p.matches, paletteMatch{
			index: p.backing[pm.Index], positions: pm.Positions})
	}
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
		c := m.commands[p.matches[p.Cursor].index]
		// Close first, then run: the command may open a prompt or a
		// confirm, which owns the same screen space the drawer held.
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

// paletteRows is how many drawer rows the palette currently costs the
// panes; zero when closed.
func (m Model) paletteRows() int {
	if m.palette == nil {
		return 0
	}
	return m.palette.Rows()
}

// paletteLines renders the drawer's match rows at the given width.
func (m Model) paletteLines(width int) []string {
	return m.palette.Lines(m.palette.items, width, m.theme)
}

// paletteDesc is the highlighted command's one-line explanation, rendered
// under the palette's help line; empty when the palette is closed, nothing
// matches, or the command carries no description.
func (m Model) paletteDesc() string {
	if m.palette == nil {
		return ""
	}
	return m.palette.Desc(m.palette.items)
}
