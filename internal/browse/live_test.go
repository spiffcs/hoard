package browse

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
)

func poll(m Model) Model {
	next, _ := m.Update(livePollMsg{})
	return next.(Model)
}

func quiet(m Model) Model {
	next, _ := m.Update(liveQuietMsg{gen: m.liveGen})
	return next.(Model)
}

func changed(st *fakeStore, m Model) Model {
	st.dataVersion++
	return poll(m)
}

func TestLivePollTakesABaselineNotARefresh(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	reads := st.binderListCalls

	m = poll(m)

	if !m.liveKnown {
		t.Error("the first poll took no baseline")
	}
	if m.liveGen != 0 {
		t.Error("the first poll armed the gate")
	}
	if st.binderListCalls != reads {
		t.Errorf("%d re-reads on the first poll, want 0", st.binderListCalls-reads)
	}
}

func TestQuiescenceGateCoalescesABurst(t *testing.T) {
	st := testStore()
	m := poll(newTestModel(t, st))
	reads := st.binderListCalls

	const burst = 10
	for range burst {
		m = changed(st, m)
	}
	if m.liveGen != burst {
		t.Fatalf("the burst armed %d timers, want %d — the gate was never re-armed",
			m.liveGen, burst)
	}

	for gen := 1; gen < m.liveGen; gen++ {
		next, _ := m.Update(liveQuietMsg{gen: gen})
		m = next.(Model)
	}
	if got := st.binderListCalls - reads; got != 0 {
		t.Fatalf("%d re-reads from superseded timers — the burst was not coalesced", got)
	}

	m = quiet(m)
	if got := st.binderListCalls - reads; got != 1 {
		t.Errorf("%d re-reads for a burst of %d changes, want 1", got, burst)
	}
}

func TestLiveRefreshRestoresByRowIdentity(t *testing.T) {
	st := testStore()
	st.collection = manyCards(20)
	m := newTestModel(t, st)
	m.focus = paneCards
	m.cursor[paneCards] = 5
	was, wasIdx := m.selectedCard().Name, m.cursor[paneCards]

	m = poll(m)

	st.collection = append(st.collection,
		row("Black Lotus", "lea", "233", finish.Nonfoil, 1, 9999))
	m = quiet(changed(st, m))

	if got := m.selectedCard().Name; got != was {
		t.Errorf("the cursor left %q and landed on %q", was, got)
	}
	if m.cursor[paneCards] != wasIdx+1 {
		t.Errorf("cursor index %d, want %d — the row moved down exactly one",
			m.cursor[paneCards], wasIdx+1)
	}
	if m.filteredCards[wasIdx].Name == was {
		t.Fatal("the insert did not move the row, so index restore would have " +
			"passed too and nothing was proven")
	}
}

func TestLiveRefreshFollowsTheRowAcrossAPage(t *testing.T) {
	st := testStore()
	st.collection = manyCards(120)
	m := newTestModel(t, st)
	m.focus = paneCards
	m.cursor[paneCards] = singleTablePageSize - 1
	was := m.selectedCard().Name

	m = poll(m)
	st.collection = append(st.collection,
		row("Black Lotus", "lea", "233", finish.Nonfoil, 1, 9999))
	m = quiet(changed(st, m))

	if m.cardsPage != 1 {
		t.Errorf("page %d, want 1 — one insert pushed that row onto page two",
			m.cardsPage)
	}
	if m.cursor[paneCards] != 0 {
		t.Errorf("cursor %d, want 0 — the row is now the first of its page",
			m.cursor[paneCards])
	}
	if got := m.selectedCard().Name; got != was {
		t.Errorf("the cursor left %q and landed on %q", was, got)
	}
}

func TestLiveRefreshSaysWhenTheSelectedRowIsGone(t *testing.T) {
	st := testStore()
	st.collection = manyCards(20)
	m := newTestModel(t, st)
	m.focus = paneCards
	m.cursor[paneCards] = 5
	was := m.selectedCard().Name

	m = poll(m)

	st.collection = append(st.collection[:5], st.collection[6:]...)
	m = quiet(changed(st, m))

	if got := m.selectedCard().Name; got == was {
		t.Fatalf("%q is still on screen, so the removal did not happen", was)
	}
	if m.cursor[paneCards] != 5 {
		t.Errorf("cursor %d, want 5 — the place is kept even when the selection is not",
			m.cursor[paneCards])
	}
	if m.cursor[paneCards] == 0 && len(m.cards) > 1 {
		t.Error("the cursor jumped to row one, silently")
	}
	if !strings.Contains(m.status, "gone") {
		t.Errorf("status %q does not say the card went away", m.status)
	}
}

func TestLiveRefreshReportsTheDelta(t *testing.T) {
	st := testStore()
	m := poll(newTestModel(t, st))

	st.totals.TotalCopies += 3
	st.totals.Value += 41.20
	m = quiet(changed(st, m))

	if !strings.Contains(m.status, "+3 cards") {
		t.Errorf("status %q does not report the copies added", m.status)
	}
	if !strings.Contains(m.status, "41.20") {
		t.Errorf("status %q does not report the value added", m.status)
	}
	if m.statusErr {
		t.Error("the delta rendered as an error")
	}

	st.totals.TotalCopies++
	st.totals.Value += 2
	m = quiet(changed(st, m))
	if !strings.Contains(m.status, "+1 card ") {
		t.Errorf("status %q does not say \"+1 card\"", m.status)
	}
}

func TestLiveRefreshTouchesHoldingsOnly(t *testing.T) {
	st := threeTableStore()
	m := poll(onWatches(t, st))
	watchReads, unpricedReads := st.watchListCalls, st.unpricedCalls
	reads := st.binderListCalls
	m = quiet(changed(st, m))

	if st.binderListCalls == reads {
		t.Fatal("no refresh happened, so nothing was proven")
	}
	if st.watchListCalls != watchReads {
		t.Errorf("the watches were re-read %d times on a tick",
			st.watchListCalls-watchReads)
	}
	if st.unpricedCalls != unpricedReads {
		t.Errorf("the unpriced holdings were re-read %d times on a tick",
			st.unpricedCalls-unpricedReads)
	}
}

func TestLiveRefreshKeepsTheWatchesScreenStill(t *testing.T) {
	st := threeTableStore()

	for i := range 40 {
		st.watches = append(st.watches,
			watchOn(fmt.Sprintf("Filler %02d", i), fmt.Sprintf("fill-%d-id", i),
				"ddd", "over", 5, price(9)))
	}
	m := poll(onWatches(t, st))
	m.focus = paneCards
	m.cursor[paneCards] = 30
	(&m).scrollIntoView()

	wasSec, wasIdx := m.watchCursorPos()
	wasOffsets, wasCursor := m.watchSecOffset, m.cursor[paneCards]
	if wasOffsets[secOvers] == 0 {
		t.Fatalf("OVERS did not overflow (offsets %v), so nothing was proven",
			wasOffsets)
	}

	m = quiet(changed(st, m))

	if gotSec, gotIdx := m.watchCursorPos(); gotSec != wasSec || gotIdx != wasIdx {
		t.Errorf("cursor moved from table %v row %d to table %v row %d",
			wasSec, wasIdx, gotSec, gotIdx)
	}
	if m.cursor[paneCards] != wasCursor {
		t.Errorf("pane cursor %d -> %d", wasCursor, m.cursor[paneCards])
	}
	if m.watchSecOffset != wasOffsets {
		t.Errorf("section offsets %v -> %v", wasOffsets, m.watchSecOffset)
	}
	if m.view != viewWatches {
		t.Errorf("view changed to %v", m.view)
	}
}

func TestLiveRefreshDefersUnderATakeover(t *testing.T) {
	st := testStore()
	m := poll(newTestModel(t, st))
	m.focus = paneCards
	m.openDetail()
	if m.mode() != modeDetail {
		t.Fatalf("mode %v, want modeDetail", m.mode())
	}
	reads := st.binderListCalls

	m = quiet(changed(st, m))

	if st.binderListCalls != reads {
		t.Errorf("%d re-reads under the overlay, want 0", st.binderListCalls-reads)
	}
	if !m.livePending {
		t.Fatal("the refresh was dropped rather than held")
	}

	m.detail, m.detailComps = nil, nil
	m = poll(m)

	if st.binderListCalls == reads {
		t.Error("the held refresh never applied after the overlay closed")
	}
	if m.livePending {
		t.Error("the pending flag survived the refresh it stands for")
	}
}

func TestLivePollSkipsTheReadBehindAnOperation(t *testing.T) {
	st := testStore()
	m := poll(newTestModel(t, st))
	reads := st.dataVersionReads
	m.op = &opState{}

	m = poll(m)

	if st.dataVersionReads != reads {
		t.Errorf("%d header reads behind an operation, want 0",
			st.dataVersionReads-reads)
	}

	m.op = nil
	st.dataVersion++
	m = poll(m)
	if m.liveGen == 0 {
		t.Error("the change that landed during the operation was never seen")
	}
}

func TestLiveRefreshRetiresWhenItGetsSlow(t *testing.T) {
	st := testStore()
	m := poll(atAllCards(t, newTestModel(t, st)))
	st.slowRead = liveRefreshBudget + 50*time.Millisecond

	m = quiet(changed(st, m))

	if !m.liveOff {
		t.Fatal("a refresh over budget did not retire the feature")
	}
	if !strings.Contains(m.status, "press r") {
		t.Errorf("status %q does not point at the manual key", m.status)
	}
	if !strings.Contains(m.status, "ms") {
		t.Errorf("status %q does not say what it measured", m.status)
	}

	st.slowRead = 0
	reads := st.binderListCalls
	m = quiet(changed(st, m))
	if st.binderListCalls != reads {
		t.Errorf("%d re-reads after retirement, want 0", st.binderListCalls-reads)
	}
	if !strings.Contains(m.status, "changed elsewhere") {
		t.Errorf("status %q does not tell the reader the hoard moved", m.status)
	}
	if !strings.Contains(m.status, "press r") {
		t.Errorf("status %q does not say what to do about it", m.status)
	}
}

func TestManualReloadStillWorksAfterRetirement(t *testing.T) {
	st := testStore()
	m := poll(atAllCards(t, newTestModel(t, st)))
	st.slowRead = liveRefreshBudget + 50*time.Millisecond
	m = quiet(changed(st, m))
	st.slowRead = 0
	if !m.liveOff {
		t.Fatal("the feature did not retire, so the fallback is untested")
	}

	reads := st.binderListCalls
	m = key(m, "r")

	if st.binderListCalls == reads {
		t.Error("r re-read nothing")
	}
	if m.status != "reloaded" {
		t.Errorf("status %q, want \"reloaded\"", m.status)
	}
}

func TestLiveRefreshIgnoresOurOwnEdits(t *testing.T) {
	st := testStore()
	m := poll(newTestModel(t, st))
	m.focus = paneCards
	m.adjustQuantity(1)
	reads := st.binderListCalls

	m = poll(m)

	if m.liveGen != 0 {
		t.Error("our own edit armed the gate")
	}
	if st.binderListCalls != reads {
		t.Errorf("%d re-reads chasing our own edit", st.binderListCalls-reads)
	}
}

func TestLiveConstantsMatchTheDesign(t *testing.T) {
	if livePollInterval != 500*time.Millisecond {
		t.Errorf("poll interval %v, design says 500ms", livePollInterval)
	}
	if liveQuietPeriod != 750*time.Millisecond {
		t.Errorf("quiet period %v, design says 750ms", liveQuietPeriod)
	}
	if liveRefreshBudget != 250*time.Millisecond {
		t.Errorf("refresh budget %v, design says 250ms", liveRefreshBudget)
	}
	if liveQuietPeriod <= livePollInterval {
		t.Error("a gate shorter than the poll cannot coalesce a burst")
	}
}

func TestLivePollChainReArms(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init armed no poll")
	}
	for _, name := range []string{"idle", "behind an op", "changed"} {
		switch name {
		case "behind an op":
			m.op = &opState{}
		case "changed":
			m.op = nil
			st.dataVersion++
		}
		next, cmd := m.Update(livePollMsg{})
		m = next.(Model)
		if cmd == nil {
			t.Errorf("%s: the poll chain stopped", name)
		}
	}
}
