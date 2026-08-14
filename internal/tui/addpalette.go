package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

type addCommand struct {
	ui.PaletteItem

	applies func(m model) bool
	run     func(m model) (tea.Model, tea.Cmd)
}

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
				Desc:    "End the session and go back to your collection. Everything added is already saved; anything still waiting for review is dropped.",
				Key:     "ctrl+d",
			},
			applies: func(m model) bool { return true },
			run:     func(m model) (tea.Model, tea.Cmd) { return m.finishAdding() },
		},
	}
}

type addPalette struct {
	ui.Palette
	items   []ui.PaletteItem
	backing []addCommand
}

func (m model) openAddPalette() (tea.Model, tea.Cmd) {
	m.addPalette = &addPalette{}
	m.status = ""
	m.refreshAddPalette()
	return m, nil
}

func (m *model) refreshAddPalette() {
	p := m.addPalette
	p.items = p.items[:0]
	p.backing = p.backing[:0]
	for _, c := range addCommands() {
		if !c.applies(*m) {
			continue
		}
		if c.Title == "Done" {

			c.Desc = "Finish adding and go back to your collection."
			if !m.embedded {
				c.Desc = "Finish adding and close the program."
			}
		}
		if c.Title == "Scan" {

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

func (m model) finishAdding() (tea.Model, tea.Cmd) {
	m.closeSession()

	m.resolveGen++
	dropped := m.resolving
	m.resolving = 0
	if m.reviewing() {

		m.review = append([]queueItem{*m.current}, m.review...)
		m.current = nil
	}
	m.walking = false
	if !m.embedded {
		dropped += len(m.review)
		m.review = nil
	}
	if dropped > 0 {
		m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
	}
	m.done = true
	return m, tea.Quit
}

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
