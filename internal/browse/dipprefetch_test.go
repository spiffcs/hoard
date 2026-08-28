package browse

import (
	"errors"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func preloadStore() *fakeStore {
	f := testStore()
	f.dips = []store.TrendRow{dipRow("Jeweled Lotus", 92.20, 75.34, 75.34)}
	f.momentum = []store.TrendRow{momentumRow("Command Tower", 28.28, 82.76, 19)}
	return f
}

func TestTrendsArePreloadedAtStartup(t *testing.T) {
	f := preloadStore()
	m := atAllCards(t, newTestModel(t, f))
	if m.view == viewDip {
		t.Fatal("the browser must not open on the dip view")
	}

	m = pump(t, m, m.Init())

	if f.dipCalls != 1 || f.momentumCalls != 1 {
		t.Fatalf("startup ran %d dip and %d momentum queries, want one each",
			f.dipCalls, f.momentumCalls)
	}
	if m.view == viewDip {
		t.Error("the background preload dragged the user onto the dip view")
	}
	if m.status != "" {
		t.Errorf("startup left the status line saying %q; the preload is meant to be quiet", m.status)
	}
}

func TestArrivingAtTheDipViewAfterStartupDoesNotRequery(t *testing.T) {
	f := preloadStore()
	m := atAllCards(t, newTestModel(t, f))
	m = pump(t, m, m.Init())

	f.err = errors.New("the preload must answer, not the store")
	for m.view != viewDip {
		m = key(m, "v")
	}

	if f.dipCalls != 1 || f.momentumCalls != 1 {
		t.Errorf("arriving re-queried: %d dip, %d momentum calls, want the preload reused",
			f.dipCalls, f.momentumCalls)
	}
	if m.statusErr {
		t.Errorf("arriving hit the store: %q", m.status)
	}
	if len(m.dips) != 1 || len(m.momentum) != 1 {
		t.Errorf("the dip view arrived empty: dips=%d momentum=%d", len(m.dips), len(m.momentum))
	}
}

func TestLeavingAndReturningToTheDipViewDoesNotRequery(t *testing.T) {
	f := preloadStore()
	m := atAllCards(t, newTestModel(t, f))
	m = pump(t, m, m.Init())
	for m.view != viewDip {
		m = key(m, "v")
	}
	for m.view != viewHoldings {
		m = key(m, "v")
	}
	for m.view != viewDip {
		m = key(m, "v")
	}

	if f.dipCalls != 1 || f.momentumCalls != 1 {
		t.Errorf("a second visit re-queried: %d dip, %d momentum calls, want one each",
			f.dipCalls, f.momentumCalls)
	}
}

func TestReloadingRefreshesThePreloadedTrends(t *testing.T) {
	f := preloadStore()
	m := atAllCards(t, newTestModel(t, f))
	m = pump(t, m, m.Init())
	for m.view != viewDip {
		m = key(m, "v")
	}

	f.dips = append(f.dips, dipRow("Mana Crypt", 200, 150, 150))
	m = pumpKey(t, m, "r")

	if len(m.dips) != 2 {
		t.Errorf("after a reload dips = %d rows, want the stale preload dropped", len(m.dips))
	}
}
