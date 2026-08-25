package browse

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/ui"
)

type cardDocumentMsg struct {
	scryfallID string
	card       scryfall.Card
}

func (m *Model) fetchCardDocument(id string) tea.Cmd {
	if m.cardDocument == nil || id == "" {
		return nil
	}
	if d := m.detail; d != nil && d.card.ScryfallID == id &&
		m.imgTier != ui.ImageNone && m.imageFetch != nil {
		d.imagePending = true
	}
	fetch, ctx := m.cardDocument, m.ctx
	return func() tea.Msg {
		card, err := fetch(ctx, id)
		if err != nil {
			return cardDocumentMsg{scryfallID: id}
		}
		return cardDocumentMsg{scryfallID: id, card: card}
	}
}

func (m Model) onCardDocument(msg cardDocumentMsg) (tea.Model, tea.Cmd) {
	d := m.detail
	onScreen := d != nil && d.card.ScryfallID == msg.scryfallID
	if onScreen {
		d.imagePending = false
	}
	if msg.card.ID == "" {
		return m, nil
	}
	if err := m.store.UpsertPrintings([]scryfall.Card{msg.card}); err != nil {
		m.setError(err)
		return m, nil
	}
	if !onScreen || !m.loadPrinting(d, msg.scryfallID) {
		return m, nil
	}
	m.refreshLinks(d)
	return m, m.fetchDetailImage()
}
