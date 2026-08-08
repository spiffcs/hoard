package tui

// The add view's command palette — the same drawer the browser opens on ':',
// over "Add cards to your collection".
//
// The name step is the one place in this cascade that is a *destination*
// rather than a step: everything else is a list, a confirm or a spinner that
// exists to be answered and left. So it is the one place a palette earns its
// keep, and the only place this opens.
//
// It shares internal/ui's palette rather than growing a second one. The
// browser's version and this one differ only in which commands they list,
// which is exactly the part a shared control should not own.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

// addCommand is one row of the name step's palette: what it looks like, when
// it is offered, and what it does.
type addCommand struct {
	ui.PaletteItem
	// applies decides whether the command lists at all. A command that
	// cannot run is absent rather than present-and-refusing — the palette is
	// meant to answer "what can I do", and a row that only ever apologises
	// is a worse answer than no row.
	applies func(m model) bool
	run     func(m model) (tea.Model, tea.Cmd)
}

// addCommands is the registry, in the order an empty query lists them.
//
// Scan leads because it is what the view is for on a rig that has a phone;
// Pair sits under it because it is the once-per-phone step that has to
// happen before scanning works at all. Done is last and is the only one that
// always applies.
func addCommands() []addCommand {
	return []addCommand{
		{
			PaletteItem: ui.PaletteItem{
				Title:   "Scan",
				Aliases: "camera iphone phone capture shutter",
				Key:     "ctrl+o",
			},
			applies: func(m model) bool { return m.scanner != nil },
			run: func(m model) (tea.Model, tea.Cmd) {
				// Two steps, not `return m, m.beginScan()`: beginScan takes a
				// pointer receiver and sets the next state, and the order of a
				// value copy against a mutating call in one return statement
				// is not specified.
				cmd := m.beginScan()
				return m, cmd
			},
		},
		{
			PaletteItem: ui.PaletteItem{
				Title:   "Pair",
				Aliases: "phone iphone code connect setup",
				Desc:    "Pair an iPhone running Hoardling, or re-pair after its code rotated.",
				Key:     "ctrl+p",
			},
			applies: func(m model) bool { return m.scanner != nil },
			run: func(m model) (tea.Model, tea.Cmd) {
				m.pairing = true
				m.state = statePairIntro
				return m, nil
			},
		},
		{
			PaletteItem: ui.PaletteItem{
				Title:   "Done",
				Aliases: "finish exit quit close collection back",
				Key:     "ctrl+d",
			},
			applies: func(m model) bool { return true },
			run:     func(m model) (tea.Model, tea.Cmd) { return m.finishAdding() },
		},
	}
}

// addPalette is the drawer's state plus the mapping back into the registry,
// the same shape the browser's uses.
type addPalette struct {
	ui.Palette
	items   []ui.PaletteItem
	backing []addCommand
}

// openAddPalette opens the drawer over the name step.
func (m model) openAddPalette() (tea.Model, tea.Cmd) {
	m.addPalette = &addPalette{}
	m.status = ""
	m.refreshAddPalette()
	return m, nil
}

// refreshAddPalette rebuilds the applicable set and re-runs the match.
//
// Rebuilt on every keystroke rather than once at open, because two of the
// three descriptions read differently depending on whether a camera session
// is already live, and a drawer that opened before one started would go on
// describing the old situation.
func (m *model) refreshAddPalette() {
	p := m.addPalette
	p.items = p.items[:0]
	p.backing = p.backing[:0]
	for _, c := range addCommands() {
		if !c.applies(*m) {
			continue
		}
		if c.Title == "Done" {
			// Where "done" lands depends on how the cascade was started, and
			// the row says which — the same honesty escWord gives esc.
			c.Desc = "Finish adding and go back to your collection."
			if !m.embedded {
				c.Desc = "Finish adding and close the program."
			}
		}
		if c.Title == "Scan" {
			// A live session is not reopened, it is returned to, and the row
			// should not promise otherwise.
			c.Desc = "Open the camera and scan cards with your iPhone."
			if m.session != nil {
				c.Desc = "Back to the camera that is already open."
			}
		}
		p.items = append(p.items, c.PaletteItem)
		p.backing = append(p.backing, c)
	}
	p.Refresh(p.items)
}

// handleAddPaletteKey drives the drawer. Keys that are not the palette's
// close it first and are not re-dispatched: a stray ctrl+o under an open
// drawer means the drawer, not the camera.
func (m model) handleAddPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.addPalette
	switch msg.Type {
	case tea.KeyEsc:
		m.addPalette = nil
		return m, nil
	case tea.KeyEnter:
		i, ok := p.Selected()
		if !ok {
			return m, nil
		}
		c := p.backing[i]
		// Close first, then run: the command may move to a step that owns
		// the same screen space the drawer held.
		m.addPalette = nil
		return c.run(m)
	case tea.KeyUp:
		p.Up()
		return m, nil
	case tea.KeyDown:
		p.Down()
		return m, nil
	case tea.KeyBackspace:
		p.Backspace()
		m.refreshAddPalette()
		return m, nil
	case tea.KeyCtrlU:
		p.Clear()
		m.refreshAddPalette()
		return m, nil
	case tea.KeySpace:
		p.Type(" ")
		m.refreshAddPalette()
		return m, nil
	case tea.KeyRunes:
		p.Type(string(msg.Runes))
		m.refreshAddPalette()
		return m, nil
	}
	return m, nil
}

// finishAdding ends the session and hands the terminal back — the browser's
// collection view when embedded, the shell when standalone.
//
// Queued cards do not block it. They used to: ctrl+d refused outright with
// "finish or ctrl+s them first", which is a refusal that makes someone hunt
// for a second key before they can leave a view they have already decided to
// leave. It asks instead, with the same gate esc has always shown, and the
// count in the prompt is what makes the answer an informed one.
func (m model) finishAdding() (tea.Model, tea.Cmd) {
	if m.reviewing() || len(m.review) > 0 || m.resolving > 0 {
		m.leaveFrom = m.state
		m.state = stateLeaveConfirm
		return m, nil
	}
	m.done = true
	m.closeSession()
	return m, tea.Quit
}

// addPaletteView renders the drawer: the query line, the match rows, the
// shared help line, and the highlighted command's description under it.
//
// Bottom-anchored under the name field rather than floating over it, for the
// reason the browser's is: a partial-width box leaves fragments of whatever
// it covers visible around its edges, and a horizontal split has none.
func (m model) addPaletteView() string {
	p := m.addPalette
	width := m.width
	if width <= 0 {
		width = 80
	}

	var b strings.Builder
	b.WriteString(strings.Repeat("─", width) + "\n")
	b.WriteString(": " + p.Query + "▏\n")
	for _, line := range p.Lines(p.items, width, m.theme) {
		b.WriteString(line + "\n")
	}
	b.WriteString(m.help(ui.PaletteHelp))
	if d := p.Desc(p.items); d != "" {
		b.WriteString("\n" + m.help(d))
	}
	return b.String()
}
