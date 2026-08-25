package browse

import (
	tea "github.com/charmbracelet/bubbletea"
)

type historyMsg struct {
	scryfallID string
	points     int
}

func (d *detail) thinHistory() bool {
	for _, s := range d.series {
		if len(s) > 1 {
			return false
		}
	}
	return true
}

func (m *Model) fetchHistory(id, setCode string) tea.Cmd {
	if m.historyBackfill == nil || id == "" {
		return nil
	}
	fill, ctx := m.historyBackfill, m.ctx
	return func() tea.Msg {
		points, err := fill(ctx, id, setCode)
		if err != nil {
			return historyMsg{scryfallID: id}
		}
		return historyMsg{scryfallID: id, points: points}
	}
}

func (m Model) onHistory(msg historyMsg) (tea.Model, tea.Cmd) {
	d := m.detail
	if msg.points == 0 || d == nil || d.card.ScryfallID != msg.scryfallID {
		return m, nil
	}
	m.loadPrinting(d, msg.scryfallID)
	return m, nil
}
