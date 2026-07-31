package store

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// reprice updates a card's stored price through the same upsert
// update-prices uses.
func reprice(t *testing.T, s *Store, price float64) {
	t.Helper()
	c := ulamog()
	c.PriceUSD = f(price)
	if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
}

func checkWatches(t *testing.T, s *Store) (fired []WatchStatus, checked int) {
	t.Helper()
	fired, checked, err := s.CheckWatches()
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	return fired, checked
}

// The crossing contract: fire when the condition starts holding — including
// the very first check — then stay quiet until it stops holding and starts
// again. A cron re-alerting daily about the same dip is the failure mode the
// last_state column exists to prevent.
func TestWatchFiresOnCrossingsOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog, the Infinite Gyre", "nonfoil", "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	// Price is 10, already under 12: the first check is the one alert.
	fired, checked := checkWatches(t, s)
	if checked != 1 || len(fired) != 1 {
		t.Fatalf("first check: fired %d of %d, want 1 of 1", len(fired), checked)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("second check re-fired without a crossing")
	}

	// Rises to 15: condition stops holding, nothing fires.
	reprice(t, s, 15)
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("leaving the threshold fired an alert")
	}

	// Falls to 11: a genuine crossing fires again.
	reprice(t, s, 11)
	fired, _ = checkWatches(t, s)
	if len(fired) != 1 {
		t.Fatalf("re-crossing fired %d alerts, want 1", len(fired))
	}
	if got := fired[0]; got.PriceUSD == nil || *got.PriceUSD != 11 || got.Op != "under" {
		t.Errorf("fired watch = %+v", got)
	}
}

// Thresholds are strict: "under 30" means below $30.00, and a price sitting
// exactly on the line satisfies neither direction.
func TestWatchThresholdsAreStrict(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 10); err != nil {
		t.Fatalf("AddWatch under: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "over", 10); err != nil {
		t.Fatalf("AddWatch over: %v", err)
	}
	if fired, checked := checkWatches(t, s); checked != 2 || len(fired) != 0 {
		t.Errorf("price exactly at both thresholds fired %d of %d watches", len(fired), checked)
	}
}

// An unpriced card answers neither question: it is not counted as checked
// and its state is untouched, so the alert fires when the price appears —
// not a phantom crossing manufactured by the gap.
func TestWatchSkipsUnpricedCards(t *testing.T) {
	s := newTestStore(t)
	card := ulamog()
	card.PriceUSD, card.PriceUSDFoil = nil, nil
	if err := s.AddCardFinish(card, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired, checked := checkWatches(t, s); checked != 0 || len(fired) != 0 {
		t.Fatalf("unpriced card was checked: fired %d of %d", len(fired), checked)
	}
	reprice(t, s, 10)
	if fired, checked := checkWatches(t, s); checked != 1 || len(fired) != 1 {
		t.Errorf("price appearing under the threshold fired %d of %d, want 1 of 1", len(fired), checked)
	}
}

// Re-adding the same question replaces the threshold instead of stacking a
// second alert, and the replacement counts as never checked.
func TestWatchReAddReplacesThreshold(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Fatal("setup: first check should fire")
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 11); err != nil {
		t.Fatalf("AddWatch replace: %v", err)
	}
	watches, err := s.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 1 || watches[0].Threshold != 11 {
		t.Fatalf("watches = %+v, want one with the new threshold", watches)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Error("the replaced watch did not fire its own first check")
	}
}

func TestWatchByRefAndRemove(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(solRing(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog, the Infinite Gyre", "nonfoil", "under", 5); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if err := s.AddWatch("sol-id", "Sol Ring", "nonfoil", "under", 1); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	w, err := s.WatchByRef("ulamog")
	if err != nil || w.ScryfallID != "ulamog-id" {
		t.Errorf("WatchByRef(ulamog) = %+v, %v", w, err)
	}
	if _, err := s.WatchByRef("nothing-here"); err == nil {
		t.Error("WatchByRef matched a fragment no watch contains")
	}
	if err := s.AddWatch("sol-id", "Sol Ring", "nonfoil", "over", 30); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if _, err := s.WatchByRef("sol"); err == nil || !strings.Contains(err.Error(), "use the id") {
		t.Errorf("ambiguous fragment error = %v, want id guidance", err)
	}

	if err := s.RemoveWatch(w.ID); err != nil {
		t.Fatalf("RemoveWatch: %v", err)
	}
	if _, err := s.WatchByRef("ulamog"); err == nil {
		t.Error("removed watch still resolves")
	}
	if err := s.RemoveWatch(w.ID); err == nil {
		t.Error("removing a removed watch succeeded")
	}
}
