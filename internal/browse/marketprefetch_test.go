package browse

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
)

func countingMarket(calls *int, res market.Result) MarketFunc {
	return func(context.Context, progress.Fn, float64) (market.Result, error) {
		*calls++
		return res, nil
	}
}

func vendorQuotes() market.Result {
	return market.Result{
		Compared:      2,
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20), opp("Liquid", 10, 9)},
	}
}

func TestMarketIsFetchedInTheBackgroundAtStartup(t *testing.T) {
	var calls int
	m := marketModel(t, countingMarket(&calls, vendorQuotes()))
	if m.view == viewMarket {
		t.Fatal("the browser must not open on the market view")
	}

	m = pump(t, m, m.Init())

	if calls != 1 {
		t.Fatalf("vendor prices fetched %d times during startup, want once", calls)
	}
	if !m.marketLoaded {
		t.Fatal("the market view is not ready once startup finishes")
	}
	if m.view == viewMarket {
		t.Error("the background fetch dragged the user onto the market view")
	}
	if m.status != "" {
		t.Errorf("startup left the status line saying %q; the fetch is meant to be quiet", m.status)
	}
}

func TestArrivingAtTheMarketAfterStartupDoesNotRefetch(t *testing.T) {
	var calls int
	m := marketModel(t, countingMarket(&calls, vendorQuotes()))
	m = pump(t, m, m.Init())

	for m.view != viewMarket {
		m = key(m, "v")
	}
	if calls != 1 {
		t.Errorf("arriving at the market fetched again: %d calls, want the startup one reused", calls)
	}
	if m.marketLoading {
		t.Error("the market view is still loading; startup should have left it ready")
	}
	if m.marketTotalRows() == 0 {
		t.Error("the market view arrived empty despite the startup fetch")
	}
}

func TestALateStartupFetchDoesNotClobberTheUsersOwn(t *testing.T) {
	var calls int
	m := marketModel(t, countingMarket(&calls, vendorQuotes()))

	mine := market.Result{Compared: 99, Opportunities: []market.Opportunity{opp("Mine", 1, 30)}}
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: mine})
	m = next.(Model)
	if !m.marketLoaded {
		t.Fatal("the user's own fetch did not land")
	}

	next, _ = m.Update(marketPrefetchMsg{res: vendorQuotes()})
	m = next.(Model)
	if m.marketResult.Compared != 99 {
		t.Errorf("a late startup fetch overwrote the user's own result (compared=%d, want 99)",
			m.marketResult.Compared)
	}
}

func TestStartupFetchIgnoresThePennyFilter(t *testing.T) {
	var mins []float64
	m := marketModel(t, func(_ context.Context, _ progress.Fn, min float64) (market.Result, error) {
		mins = append(mins, min)
		return vendorQuotes(), nil
	})
	m.showPennies, m.pennyLimit = false, 5

	m = pump(t, m, m.Init())

	if len(mins) != 1 || mins[0] != 0 {
		t.Errorf("startup fetched with min %v, want [0] so the cache holds every quote", mins)
	}
}

func TestStartupFetchStillHonoursTheFloorOnScreen(t *testing.T) {
	var asked []float64
	m, err := New(testStore(),
		WithMarket(func(_ context.Context, _ progress.Fn, min float64) (market.Result, error) {
			return market.Result{Compared: int(min)}, nil
		}),
		WithMarketCached(func(min float64) (market.Result, bool) {
			asked = append(asked, min)
			return market.Result{Compared: int(min)}, true
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	m = next.(Model)
	m.showPennies, m.pennyLimit = false, 5

	m = pump(t, m, m.Init())

	if len(asked) == 0 || asked[0] != 0 {
		t.Errorf("the warm cache was primed at %v, want 0 first", asked)
	}
	if m.marketResult.Compared != 5 {
		t.Errorf("the screen shows the unfiltered result (compared=%d), want the floor of 5 applied",
			m.marketResult.Compared)
	}
}
