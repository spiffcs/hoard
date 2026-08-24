package browse

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

func (m Model) folderNamed(name string) *container {
	for i := range m.containers {
		if m.containers[i].Kind == kindFolder &&
			strings.EqualFold(m.containers[i].Name, name) {
			return &m.containers[i]
		}
	}
	return nil
}

func (m *Model) promptMoveDeck() {
	sel := m.selectedContainer()
	switch {
	case m.focus != paneContainers || sel == nil:
		m.status, m.statusErr = "select a deck to move (tab to the left pane)", true
		return
	case sel.Kind == kindFolder:
		m.status, m.statusErr = sel.Name+
			" is a folder · folders hold decks, not other folders", true
		return
	case sel.Kind != store.KindDeck:
		m.status, m.statusErr = "only decks go in folders · select a deck", true
		return
	}
	id, name := sel.ID, sel.Name
	m.prompt = &prompt{
		label: fmt.Sprintf("move %q into", name),
		help:  "a folder name · blank moves it to the top level · enter accept · esc cancel",
		commit: func(m *Model, text string) tea.Cmd {
			return m.commitDeckMove(id, name, text)
		},
	}
}

func (m *Model) commitDeckMove(deckID int64, deckName, text string) tea.Cmd {
	want := strings.TrimSpace(text)
	if want == "" {
		m.applyDeckMove(deckID, deckName, 0, "")
		return nil
	}
	if f := m.folderNamed(want); f != nil {
		m.applyDeckMove(deckID, deckName, f.ID, f.Name)
		return nil
	}
	m.confirm = &pendingConfirm{
		prompt: fmt.Sprintf("no folder %q · create it and move %q into it?", want, deckName),
		help:   "y create · any other key cancels",
		onYes: func(m *Model) tea.Cmd {
			id, err := m.store.CreateFolder(want)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.applyDeckMove(deckID, deckName, id, want)
			return nil
		},
	}
	return nil
}

func (m *Model) applyDeckMove(deckID int64, deckName string, folderID int64, folderName string) {
	if err := m.store.MoveDeckToFolder(deckID, folderID); err != nil {
		m.status, m.statusErr = err.Error(), true
		return
	}
	if folderID != 0 && m.collapsed[folderID] {
		m.setFolded(folderID, false)
		m.persistFoldState()
	}
	if err := m.reread(rereadAll); err != nil {
		m.setError(err)
		return
	}
	if i := m.containerIndexOf(deckID); i >= 0 {
		if err := m.selectContainer(i); err != nil {
			m.setError(err)
			return
		}
		m.displacedContainer = 0
	}
	if folderID == 0 {
		m.status, m.statusErr = "moved "+deckName+" to the top level", false
		return
	}
	m.status, m.statusErr = "moved "+deckName+" into "+folderName, false
}

func (m *Model) promptNewFolder() {
	m.prompt = &prompt{
		label: "new folder",
		help:  "a name for grouping decks · enter accept · esc cancel",
		commit: func(m *Model, text string) tea.Cmd {
			name := strings.TrimSpace(text)
			if name == "" {
				m.status, m.statusErr = "a folder needs a name", true
				return nil
			}
			if _, err := m.store.CreateFolder(name); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			if err := m.reread(rereadAll); err != nil {
				m.setError(err)
				return nil
			}
			m.status, m.statusErr = "created folder "+name, false
			return nil
		},
	}
}
