package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func pennyStore() *fakeStore {
	f := testStore()
	f.movers = []store.PriceChange{
		mover("Bitterblossom-id", finish.Nonfoil, 4, 30, 34),
		mover("Sol Ring-id", finish.Nonfoil, 1, 0.15, 0.18),
	}
	f.dips = []store.TrendRow{
		dipRow("Jeweled Lotus", 92.20, 75.34, 75.34),
		dipRow("Chromatic Star", 0.90, 0.20, 0.20),
	}
	f.momentum = []store.TrendRow{
		momentumRow("Command Tower", 28.28, 82.76, 19),
		momentumRow("Evolving Wilds", 0.10, 0.35, 5),
	}
	return f
}

func pennyQuotes(min float64) (market.Result, bool) {
	var res market.Result
	for _, c := range []market.Comp{comp("Bulk", 0.30, 0.30, 0.10), comp("Dear", 60, 55, 44)} {
		if c.Low >= min {
			res.Comps = append(res.Comps, c)
		}
	}
	res.Compared = len(res.Comps)
	return res, true
}

func pennyModel(t *testing.T, f *fakeStore) Model {
	t.Helper()
	m, err := New(f, WithEnv(ui.Env{Color: true}), WithMarketCached(pennyQuotes))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.setsMode = false
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	return atAllCards(t, next.(Model))
}

func toView(t *testing.T, m Model, v viewMode) Model {
	t.Helper()
	for i := 0; m.view != v; i++ {
		if i > len(viewCycle) {
			t.Fatalf("never reached the %v view", v)
		}
		m = pumpKey(t, m, "v")
	}
	return m
}

func TestOnePennyLineGovernsEveryPriceView(t *testing.T) {
	m := pennyModel(t, pennyStore())

	m = toView(t, m, viewMovers)
	m = runByID(t, m, "pennies.limit")
	if m.prompt == nil {
		t.Fatal("SetPennyFilter must open a prompt")
	}
	m.prompt.commit(&m, "0.55")
	m.prompt = nil

	if len(m.movers) != 1 || m.movers[0].Name != "Bitterblossom" {
		t.Errorf("movers at $0.55 = %d rows, want the $0.18 row gated", len(m.movers))
	}

	m = toView(t, m, viewDip)
	if got := dipNames(m.dips); len(got) != 1 || got[0] != "Jeweled Lotus" {
		t.Errorf("dips = %v, want the one line set on movers to reach the dip table", got)
	}
	if got := dipNames(m.momentum); len(got) != 1 || got[0] != "Command Tower" {
		t.Errorf("momentum = %v, want the one line set on movers to reach it too", got)
	}

	m = toView(t, m, viewMarket)
	if got := compNames(m.marketComps); len(got) != 1 || got[0] != "Dear" {
		t.Errorf("comps = %v, want the one line set on movers to reach the market too", got)
	}

	if m.pennyLimit != 0.55 {
		t.Errorf("pennyLimit = %v after three views, want the single line unchanged", m.pennyLimit)
	}
}

func TestPennyToggleReachesEveryPriceView(t *testing.T) {
	m := pennyModel(t, pennyStore())

	for _, v := range []viewMode{viewMovers, viewMarket, viewDip} {
		m = toView(t, m, v)
		m = runByID(t, m, "pennies.toggle")
		if !m.showPennies {
			t.Fatalf("the toggle did not disarm the gate on the %v view", v)
		}
		if !strings.Contains(m.status, "penny filter ≤ $1.00 off") {
			t.Errorf("%v status = %q, want the shared off receipt", v, m.status)
		}
		m = runByID(t, m, "pennies.toggle")
		if m.showPennies {
			t.Fatalf("the toggle did not re-arm the gate on the %v view", v)
		}
		if !strings.Contains(m.status, "penny filter ≤ $1.00 on") {
			t.Errorf("%v status = %q, want the shared on receipt", v, m.status)
		}
	}
}

func TestPennyFilterIsNotOfferedWhereItWouldMislead(t *testing.T) {
	m := pennyModel(t, pennyStore())

	for _, v := range []viewMode{viewHoldings, viewWatches} {
		m = toView(t, m, v)
		for _, id := range []string{"pennies.toggle", "pennies.limit"} {
			for _, c := range m.commands {
				if c.id == id && c.applies(&m) {
					t.Errorf("%q is offered on the %v view; it hides rows the totals still count", id, v)
				}
			}
		}
	}
}

func TestPennyFilterPersistsUnderOneKey(t *testing.T) {
	f := pennyStore()
	m := pennyModel(t, f)

	m = toView(t, m, viewMovers)
	m = runByID(t, m, "pennies.limit")
	m.prompt.commit(&m, "0.55")
	m.prompt = nil
	m = runByID(t, m, "pennies.toggle")

	if f.settings[setPennyLimit] != "0.55" || f.settings[setShowPennies] != "true" {
		t.Fatalf("stored = %v, want one limit and one toggle under the shared keys", f.settings)
	}

	back := pennyModel(t, f)
	if back.pennyLimit != 0.55 || !back.showPennies {
		t.Errorf("restored limit %v showPennies %v, want the stored session back",
			back.pennyLimit, back.showPennies)
	}

	f.settings[setPennyLimit] = "over 9000"
	garbled := pennyModel(t, f)
	if garbled.pennyLimit != defaultPennyLimit {
		t.Errorf("garbled limit restored as %v, want the default", garbled.pennyLimit)
	}
}

func TestPennyFilterAdoptsTheOldPerViewSetting(t *testing.T) {
	f := pennyStore()
	f.settings = map[string]string{
		setMoversPennyLine: "0.75",
		setMoversPennies:   "true",
	}

	m := pennyModel(t, f)
	if m.pennyLimit != 0.75 {
		t.Errorf("pennyLimit = %v, want the old movers line carried forward", m.pennyLimit)
	}
	if !m.showPennies {
		t.Errorf("showPennies = %v, want the old movers toggle carried forward", m.showPennies)
	}
}
