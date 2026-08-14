package tui

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

	if m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied {
		title = lipgloss.StyleRunes(title, m.MatchesForItem(index),
			d.theme.Title, lipgloss.NewStyle())
	}

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

const unknownPips = "—"
