package browse

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

func windowedTrendStore() *fakeStore {
	f := testStore()
	f.trendsFor = func(o store.TrendOptions) ([]store.TrendRow, []store.TrendRow) {
		day := o.Since[:len("2006-01-02")]
		return []store.TrendRow{dipRow("Dip "+day, 92.20, 75.34, 75.34)},
			[]store.TrendRow{momentumRow("Climb "+day, 28.28, 82.76, 19)}
	}
	return f
}

func keyCmd(m Model, k string) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	return next.(Model), cmd
}

func showDipView(t *testing.T, m Model) Model {
	t.Helper()
	for range len(viewCycle) {
		if m.view == viewDip {
			return m
		}
		m = pumpKey(t, m, "v")
	}
	t.Fatalf("never reached the dip view, landed on %v", m.view)
	return m
}

func pumpKey(t *testing.T, m Model, k string) Model {
	t.Helper()
	m, cmd := keyCmd(m, k)
	return pump(t, m, cmd)
}

func onWarmDipView(t *testing.T, f *fakeStore) Model {
	t.Helper()
	m := newTestModel(t, f)
	m.clock = func() time.Time { return pinnedNow }
	m = atAllCards(t, m)
	m = pump(t, m, m.Init())
	for m.view != viewDip {
		m = key(m, "v")
	}
	if len(m.dips) == 0 {
		t.Fatal("the dip view opened with no rows to hold on to")
	}
	return m
}

func visibleTrendNames(m Model) string {
	var b strings.Builder
	for _, r := range m.dips {
		b.WriteString(r.Name + " ")
	}
	for _, r := range m.momentum {
		b.WriteString(r.Name + " ")
	}
	return b.String()
}

func TestTrendCutoffSnapsToADayBoundary(t *testing.T) {
	m := onWarmDipView(t, windowedTrendStore())

	if got, want := m.trendOptions().Since, "2026-07-12T00:00:00Z"; got != want {
		t.Errorf("Since = %q, want %q — a rolling cutoff never reuses a warmed window",
			got, want)
	}
}

func TestAColdLookbackPressNeverBlocks(t *testing.T) {
	f := windowedTrendStore()
	m := onWarmDipView(t, f)
	held := visibleTrendNames(m)
	before := f.dipCalls

	m, cmd := keyCmd(m, "W")

	if f.dipCalls != before {
		t.Errorf("W ran %d store queries on the main loop; it must hand them to a command",
			f.dipCalls-before)
	}
	if cmd == nil {
		t.Fatal("W returned no command, so the new window is never read")
	}
	if !m.trendBusy() {
		t.Error("W left no sign that a read is in flight")
	}
	if got := visibleTrendNames(m); got != held {
		t.Errorf("W blanked the tables while loading: %q, want the old rows %q", got, held)
	}

	m = pump(t, m, cmd)

	if f.dipCalls == before {
		t.Fatal("the command never read the new window")
	}
	if m.trendBusy() {
		t.Error("the rows arrived but the busy state stuck")
	}
	if got := visibleTrendNames(m); !strings.Contains(got, "2026-05-13") {
		t.Errorf("rows after loading = %q, want the 90 day window (since 2026-05-13)", got)
	}
}

func TestEveryLookbackArrivesWithoutBlocking(t *testing.T) {
	f := windowedTrendStore()
	m := onWarmDipView(t, f)

	for _, want := range []int{90, 7, 30} {
		before := f.dipCalls
		var cmd tea.Cmd
		m, cmd = keyCmd(m, "W")
		if f.dipCalls != before {
			t.Fatalf("the %d day press blocked the main loop", want)
		}
		m = pump(t, m, cmd)
		if !strings.Contains(m.status, fmt.Sprintf("%d days", want)) {
			t.Errorf("status = %q, want the %d day lookback named", m.status, want)
		}
		if len(m.dips) == 0 || len(m.momentum) == 0 {
			t.Errorf("the %d day window arrived empty: dips=%d momentum=%d",
				want, len(m.dips), len(m.momentum))
		}
	}
}

func TestARereadRewarmsTheTrendCache(t *testing.T) {
	f := windowedTrendStore()
	m := onWarmDipView(t, f)

	before := f.dipCalls
	m, cmd := keyCmd(m, "r")
	if f.dipCalls != before {
		t.Errorf("the reread ran %d store queries on the main loop; it must hand them to a command",
			f.dipCalls-before)
	}
	m = pump(t, m, cmd)

	if len(m.dips) == 0 {
		t.Fatal("the dip view came back empty after a reread")
	}
	warm := f.dipCalls
	f.err = errors.New("a rewarmed cache must answer, not the store")

	for range len(viewCycle) {
		m = key(m, "v")
	}
	if m.view != viewDip {
		t.Fatalf("did not return to the dip view, landed on %v", m.view)
	}
	if f.dipCalls != warm {
		t.Errorf("returning after a reread cost %d more queries; the reread must rewarm",
			f.dipCalls-warm)
	}
	if m.statusErr {
		t.Errorf("returning after a reread hit the store: %q", m.status)
	}
}
