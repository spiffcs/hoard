package browse

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const (
	livePollInterval = 500 * time.Millisecond

	liveQuietPeriod = 750 * time.Millisecond

	liveRefreshBudget = 250 * time.Millisecond
)

type livePollMsg struct{}

type liveQuietMsg struct{ gen int }

func livePoll() tea.Cmd {
	return tea.Tick(livePollInterval, func(time.Time) tea.Msg { return livePollMsg{} })
}

func (m *Model) armLive() tea.Cmd {
	m.liveGen++
	gen := m.liveGen
	return tea.Tick(liveQuietPeriod, func(time.Time) tea.Msg {
		return liveQuietMsg{gen: gen}
	})
}

func (m Model) onLivePoll() (tea.Model, tea.Cmd) {
	next := livePoll()

	if m.liveGone {
		return m, nil
	}

	if m.op != nil {
		return m, next
	}

	if m.livePending && m.mode() == modeBrowse {
		m.livePending = false
		m.applyLive()
	}
	v, err := m.store.DataVersion()
	if err != nil {
		if errors.Is(err, store.ErrDatabaseReplaced) {
			m.liveGone, m.liveOff = true, true
			m.status = "the database this hoard opened was replaced · restart hoard to follow it"
			m.statusErr = true
			return m, nil
		}
		return m, next
	}
	if !m.liveKnown {

		m.liveVersion, m.liveKnown = v, true
		return m, next
	}
	if v == m.liveVersion {
		return m, next
	}
	m.liveVersion = v
	if m.liveOff {
		m.liveMissed++
		m.noteLiveRetired()
		return m, next
	}
	return m, tea.Batch(next, m.armLive())
}

func (m Model) onLiveQuiet(msg liveQuietMsg) (tea.Model, tea.Cmd) {

	if msg.gen != m.liveGen || m.liveOff {
		return m, nil
	}
	if m.mode() != modeBrowse || m.op != nil {

		m.livePending = true
		return m, nil
	}
	m.applyLive()
	return m, nil
}

func (m *Model) applyLive() {
	wasCopies, wasValue := m.hoardTotals()
	start := time.Now()
	if err := m.reread(rereadLive); err != nil {
		m.setError(err)
		return
	}
	took := time.Since(start)

	copies, value := m.hoardTotals()
	m.noteLiveDelta(copies-wasCopies, value-wasValue)

	if took > liveRefreshBudget {

		m.liveOff = true
		m.status = fmt.Sprintf("live refresh off: a re-read took %dms · press r to re-read",
			took.Milliseconds())
		m.statusErr = false
	}
}

func (m *Model) noteLiveDelta(copies int, value float64) {
	m.statusErr = false
	switch {
	case m.rowGone:

		m.status = "the card you were on is gone · cursor moved to the nearest row"
	case copies != 0:
		noun := "cards"
		if copies == 1 || copies == -1 {
			noun = "card"
		}
		m.status = fmt.Sprintf("%+d %s · %s", copies, noun, ui.SignedMoney(value))
	case value != 0:

		m.status = "prices changed · " + ui.SignedMoney(value)
	default:
		m.status = "refreshed"
	}
}

func (m *Model) noteLiveRetired() {
	m.status, m.statusErr = "changed elsewhere · press r to re-read", false
	if m.liveMissed > 1 {
		m.status = fmt.Sprintf("changed elsewhere ×%d · press r to re-read", m.liveMissed)
	}
}

func (m Model) hoardTotals() (int, float64) {
	if len(m.containers) > 0 && m.containers[0].Kind == kindAllCards {
		return m.containers[0].Copies, m.containers[0].Value
	}
	return 0, 0
}
