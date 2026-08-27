package browse

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

type trendPrefetchStartMsg struct{}

type trendPrefetchMsg struct {
	key      trendKey
	gen      int
	dips     []store.TrendRow
	momentum []store.TrendRow
	err      error
}

type trendKey struct {
	daysIdx int
	settled string
}

type trendPair struct {
	dips     []store.TrendRow
	momentum []store.TrendRow
}

func (m Model) trendKey() trendKey {
	return trendKey{daysIdx: m.moversDaysIdx, settled: m.trendOptions().SettledBefore}
}

func (m Model) cachedTrends(k trendKey) (trendPair, bool) {
	if m.trendCache == nil || m.trendCacheGen != m.dataGen {
		return trendPair{}, false
	}
	pair, ok := m.trendCache[k]
	return pair, ok
}

func (m *Model) cacheTrends(k trendKey, gen int, pair trendPair) {
	if m.trendCache == nil || m.trendCacheGen != gen {
		m.trendCache = map[trendKey]trendPair{}
		m.trendCacheGen = gen
	}
	m.trendCache[k] = pair
}

func (m Model) readTrends() (trendPair, error) {
	o := m.trendOptions()
	dips, err := m.store.Dips(o)
	if err != nil {
		return trendPair{}, err
	}
	momentum, err := m.store.Momentum(o)
	if err != nil {
		return trendPair{}, err
	}
	return trendPair{dips: dips, momentum: momentum}, nil
}

func (m Model) prefetchTrends() tea.Cmd {
	k, gen, read := m.trendKey(), m.dataGen, m.readTrends
	return func() tea.Msg {
		pair, err := read()
		return trendPrefetchMsg{key: k, gen: gen, dips: pair.dips,
			momentum: pair.momentum, err: err}
	}
}

func (m Model) onTrendPrefetch(msg trendPrefetchMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	m.cacheTrends(msg.key, msg.gen, trendPair{dips: msg.dips, momentum: msg.momentum})
	if m.view == viewDip && msg.gen == m.dataGen && len(m.allDips)+len(m.allMomentum) == 0 {
		m.allDips, m.allMomentum = msg.dips, msg.momentum
		m.deriveView()
		m.clampCursor(paneCards)
	}
	return m, nil
}
