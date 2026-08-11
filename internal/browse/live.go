package browse

// The live refresh: cards added in another terminal appear here, without the
// reader pressing anything.
//
// Built to docs/specs/live-browse-refresh.md, which measured it against the
// owner's real database with a second process writing. Three facts from that
// document decide everything in this file, and each is worth knowing before
// changing anything here.
//
// One. Detection is free and refreshing is not. `PRAGMA data_version` is a
// header read — 6µs, flat in the size of the hoard — and a reader that polls
// it and never refreshes costs the writer nothing measurable. A holdings
// refresh is 41ms today and 526ms at ten times the hoard, and under
// journal_mode=DELETE a committing writer waits for whatever read is in
// flight. So the poll is not the expensive half; the read it triggers is.
//
// Two. The quiescence gate is the feature. A change arms a timer rather than
// triggering a read, and a further change re-arms it, so a burst of adds
// costs one refresh at the end instead of one apiece. Measured under a
// writer adding ten cards a second: ungated, 18 of 60 writes stalled past
// 10ms with a worst case of 63ms; gated, 0 of 60 and a p50 of 4.8ms against
// a no-reader baseline of 4.4ms. Nothing else in this file is load-bearing
// in the same way, and in particular the poll interval is not a tuning knob
// — stretching it changes the odds of a collision, never its cost.
//
// Three. The interesting failure is not contention, it is growth. The gate
// keeps a slow refresh off the writer, but the browser holds a single
// connection, so a long read still parks the reader's own keystrokes behind
// it. Hence liveRefreshBudget: the feature times itself and retires when it
// stops being free, which is how it can be always-on with no flag and no
// config key to get wrong.

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

const (
	// livePollInterval is how often the header read runs.
	//
	// 500ms rather than something sharper because the measurements say a
	// shorter interval buys nothing below the gate's own latency: perceived
	// delay is poll + gate either way, so halving this only doubles the
	// frame cost to sharpen a number nobody can feel. A tick wakes the Elm
	// loop and bubbletea renders a frame after every message — measured at
	// ~212µs, so this cadence is 0.04% of one core on an idle browser.
	livePollInterval = 500 * time.Millisecond

	// liveQuietPeriod is how long the stream of changes must be still
	// before the refresh runs. This is the gate; see the file comment.
	liveQuietPeriod = 750 * time.Millisecond

	// liveRefreshBudget is how long one refresh may take before the feature
	// retires itself for the session. 250ms is where a keypress stops
	// feeling immediate, and a refresh holds the browser's only connection
	// for its whole duration. Reasoned rather than measured — the design
	// says so too, and someone sitting in front of a hoard big enough to
	// trip it should move this number to where it actually starts to hurt.
	liveRefreshBudget = 250 * time.Millisecond
)

// livePollMsg is one poll tick. The chain re-arms itself from onLivePoll, so
// there is exactly one in flight for the life of the program.
type livePollMsg struct{}

// liveQuietMsg is a quiescence timer coming due. gen is what makes the gate
// a gate: each change arms a newer timer, and every older one is ignored on
// arrival — the same generation idiom the kitty retransmit tick uses.
type liveQuietMsg struct{ gen int }

// livePoll schedules the next header read.
func livePoll() tea.Cmd {
	return tea.Tick(livePollInterval, func(time.Time) tea.Msg { return livePollMsg{} })
}

// armLive starts — or restarts — the quiescence timer.
func (m *Model) armLive() tea.Cmd {
	m.liveGen++
	gen := m.liveGen
	return tea.Tick(liveQuietPeriod, func(time.Time) tea.Msg {
		return liveQuietMsg{gen: gen}
	})
}

// onLivePoll reads the change counter and, when it moved, arms the gate.
func (m Model) onLivePoll() (tea.Model, tea.Cmd) {
	next := livePoll()
	// An operation owns the single connection, and a PRAGMA queued behind
	// an update-prices is not a 6µs read. Skip the reading, keep the chain:
	// data_version is a counter, not an event, so anything that happens
	// while we are not looking is still there in the next reading we take.
	if m.op != nil {
		return m, next
	}
	// A refresh that came due behind a takeover applies here. This is the
	// only place that notices the keyboard came back, which is why deferral
	// costs one condition rather than a hook on the way out of every
	// surface that can own it.
	if m.livePending && m.mode() == modeBrowse {
		m.livePending = false
		m.applyLive()
	}
	v, err := m.store.DataVersion()
	if err != nil {
		// A failed header read is not worth a status line nobody asked
		// for, and the next tick will try again.
		return m, next
	}
	if !m.liveKnown {
		// The first reading is the baseline, not a change.
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

// onLiveQuiet is the gate opening: the change stream has been still for
// liveQuietPeriod.
func (m Model) onLiveQuiet(msg liveQuietMsg) (tea.Model, tea.Cmd) {
	// A change that arrived after this timer was armed re-armed it, and
	// this is the superseded copy. Firing it anyway is exactly the burst
	// behavior the gate exists to prevent.
	if msg.gen != m.liveGen || m.liveOff {
		return m, nil
	}
	if m.mode() != modeBrowse || m.op != nil {
		// Hold it. A detail overlay, the filter bar, a staged confirm or
		// the add cascade all mean the reader is looking at something that
		// must not move under them — including the overlay's own contents,
		// which are read once when it opens and would otherwise go stale
		// against panes that had just moved on.
		m.livePending = true
		return m, nil
	}
	m.applyLive()
	return m, nil
}

// applyLive is the refresh itself: holdings and containers, timed against
// the budget that retires the feature.
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
		// The hoard has outgrown the feature. Say so once, with the number
		// that decided it, and fall back to the key that was always there
		// — a browser that has quietly become sticky is worse than one
		// that says why.
		m.liveOff = true
		m.status = fmt.Sprintf("live refresh off: a re-read took %dms · press r to re-read",
			took.Milliseconds())
		m.statusErr = false
	}
}

// noteLiveDelta says on the status line what the refresh brought in, so a
// change that lands below the fold is still legible.
func (m *Model) noteLiveDelta(copies int, value float64) {
	m.statusErr = false
	switch {
	case m.rowGone:
		// The selection could not survive and should not pretend to. The
		// cursor is on the nearest surviving row, which is the honest
		// meaning of keeping your place.
		m.status = "the card you were on is gone · cursor moved to the nearest row"
	case copies != 0:
		noun := "cards"
		if copies == 1 || copies == -1 {
			noun = "card"
		}
		m.status = fmt.Sprintf("%+d %s · %s", copies, noun, ui.SignedMoney(value))
	case value != 0:
		// Prices moved without the holdings changing — an update-prices in
		// the other terminal.
		m.status = "prices changed · " + ui.SignedMoney(value)
	default:
		m.status = "refreshed"
	}
}

// noteLiveRetired is the standing notice once the feature has retired: the
// hoard is still changing elsewhere, and r is how to see it.
func (m *Model) noteLiveRetired() {
	m.status, m.statusErr = "changed elsewhere · press r to re-read", false
	if m.liveMissed > 1 {
		m.status = fmt.Sprintf("changed elsewhere ×%d · press r to re-read", m.liveMissed)
	}
}

// hoardTotals is the whole hoard's copies and value, which both container
// loaders already summed onto the synthetic All Cards row that leads the
// left pane. Free, and correct whichever lens that pane is showing.
func (m Model) hoardTotals() (int, float64) {
	if len(m.containers) > 0 && m.containers[0].Kind == kindAllCards {
		return m.containers[0].Copies, m.containers[0].Value
	}
	return 0, 0
}
