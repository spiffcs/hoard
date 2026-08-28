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

type trendAttempt struct {
	key  trendKey
	gen  int
	live bool
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
	dips, momentum, err := m.store.Trends(m.trendOptions())
	if err != nil {
		return trendPair{}, err
	}
	return trendPair{dips: dips, momentum: momentum}, nil
}

func (a trendAttempt) covers(k trendKey, gen int) bool {
	return a.live && a.gen == gen && a.key == k
}

func (m Model) trendBusy() bool {
	return m.trendReading.live && m.trendReading.gen == m.dataGen
}

func (m *Model) syncTrends() tea.Cmd {
	if m.view != viewDip {
		return nil
	}
	k := m.trendKey()
	if _, ok := m.cachedTrends(k); ok {
		return nil
	}
	if m.trendReading.covers(k, m.dataGen) || m.trendFailed.covers(k, m.dataGen) {
		return nil
	}
	m.trendReading = trendAttempt{key: k, gen: m.dataGen, live: true}
	return tea.Batch(m.spinner.Tick, m.prefetchTrends())
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
	if msg.gen != m.dataGen {
		return m, nil
	}
	if m.trendReading.covers(msg.key, msg.gen) {
		m.trendReading = trendAttempt{}
	}
	if msg.err != nil {
		m.trendFailed = trendAttempt{key: msg.key, gen: msg.gen, live: true}
		return m, nil
	}
	m.cacheTrends(msg.key, msg.gen, trendPair{dips: msg.dips, momentum: msg.momentum})
	if m.view == viewDip && msg.key == m.trendKey() {
		m.allDips, m.allMomentum = msg.dips, msg.momentum
		m.deriveView()
		m.clampCursor(paneCards)
	}
	return m, nil
}
