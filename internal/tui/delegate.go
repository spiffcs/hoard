package tui

// cascadeDelegate renders the cascade's picker rows in hoard's own voice:
// one line per item — an accent ▌ marks the selection, identity pips
// follow a printing, annotations go dim. It replaces the bubbles default
// delegate, whose two-line purple rendering was the one place hoard
// looked like somebody else's application.

import (
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/spiffcs/hoard/internal/ui"
)

type cascadeDelegate struct{ theme ui.Theme }

func (d cascadeDelegate) Height() int                         { return 1 }
func (d cascadeDelegate) Spacing() int                        { return 0 }
func (d cascadeDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d cascadeDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	type titled interface {
		Title() string
		Description() string
	}
	it, ok := item.(titled)
	if !ok || m.Width() <= 0 {
		return
	}

	title := it.Title()
	// While a filter narrows the list, the matched runes go bold so the
	// reader can see why each row survived.
	if m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied {
		title = lipgloss.StyleRunes(title, m.MatchesForItem(index),
			d.theme.Title, lipgloss.NewStyle())
	}

	// A printing row carries its card's identity — pips beside the name,
	// the same letters the browse tables show. Unknown identity shows
	// nothing rather than a dash: a picker row is not a table cell.
	if p, ok := item.(printItem); ok {
		if s := ui.Pips(p.card.ColorIdentity); s != unknownPips {
			title += " " + d.theme.PipString(s)
		}
	}

	line := "  " + title
	if index == m.Index() {
		line = d.theme.Accent.Render("▌ ") + title
	}
	if desc := it.Description(); desc != "" {
		line += d.theme.Help.Render("  " + desc)
	}
	io.WriteString(w, ui.Truncate(line, m.Width()))
}

// unknownPips is ui.Pips' unknown marker; named so the intent ("suppress
// the dash here") survives beside the format vocabulary it mirrors.
const unknownPips = "—"
