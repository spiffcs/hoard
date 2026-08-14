package store

import (
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

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

func TestWatchFiresOnCrossingsOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog, the Infinite Gyre", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	fired, checked := checkWatches(t, s)
	if checked != 1 || len(fired) != 1 {
		t.Fatalf("first check: fired %d of %d, want 1 of 1", len(fired), checked)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("second check re-fired without a crossing")
	}

	reprice(t, s, 15)
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("leaving the threshold fired an alert")
	}

	reprice(t, s, 11)
	fired, _ = checkWatches(t, s)
	if len(fired) != 1 {
		t.Fatalf("re-crossing fired %d alerts, want 1", len(fired))
	}
	if got := fired[0]; got.PriceUSD == nil || *got.PriceUSD != 11 || got.Op != "under" {
		t.Errorf("fired watch = %+v", got)
	}
}

func TestWatchThresholdsAreStrict(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 10); err != nil {
		t.Fatalf("AddWatch under: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "over", 10); err != nil {
		t.Fatalf("AddWatch over: %v", err)
	}
	if fired, checked := checkWatches(t, s); checked != 2 || len(fired) != 0 {
		t.Errorf("price exactly at both thresholds fired %d of %d watches", len(fired), checked)
	}
}

func TestWatchSkipsUnpricedCards(t *testing.T) {
	s := newTestStore(t)
	card := ulamog()
	card.PriceUSD, card.PriceUSDFoil = nil, nil
	if err := s.AddCardFinish(card, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
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

func TestWatchReAddReplacesThreshold(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Fatal("setup: first check should fire")
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 11); err != nil {
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
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(solRing(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog, the Infinite Gyre", finish.Nonfoil, "under", 5); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if err := s.AddWatch("sol-id", "Sol Ring", finish.Nonfoil, "under", 1); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	w, err := s.WatchByRef("ulamog")
	if err != nil || w.ScryfallID != "ulamog-id" {
		t.Errorf("WatchByRef(ulamog) = %+v, %v", w, err)
	}
	if _, err := s.WatchByRef("nothing-here"); err == nil {
		t.Error("WatchByRef matched a fragment no watch contains")
	}
	if err := s.AddWatch("sol-id", "Sol Ring", finish.Nonfoil, "over", 30); err != nil {
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

func TestAddWatchesTalliesAndReArms(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog, the Infinite Gyre", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Fatal("setup: the standing watch should have fired once")
	}

	created, updated, err := s.AddWatches([]WatchInput{
		{ScryfallID: "ulamog-id", Display: "Ulamog, the Infinite Gyre", Finish: finish.Nonfoil, Op: "under", Threshold: 11},
		{ScryfallID: "sol-id", Display: "Sol Ring", Finish: finish.Foil, Op: "over", Threshold: 6},
	})
	if err != nil {
		t.Fatalf("AddWatches: %v", err)
	}
	if created != 1 || updated != 1 {
		t.Fatalf("created=%d updated=%d, want 1 and 1", created, updated)
	}
	watches, err := s.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 2 {
		t.Fatalf("watches = %d, want 2", len(watches))
	}

	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Error("the adjusted watch did not re-arm")
	}
}

func TestAddWatchesRollsBackOnInvalidRow(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{ulamog()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	_, _, err := s.AddWatches([]WatchInput{
		{ScryfallID: "ulamog-id", Display: "Ulamog", Finish: finish.Nonfoil, Op: "under", Threshold: 12},
		{ScryfallID: "ulamog-id", Display: "Ulamog", Finish: finish.Nonfoil, Op: "below", Threshold: 9},
	})
	if err == nil || !strings.Contains(err.Error(), `watch op must be under, over, drop or rise, not "below"`) {
		t.Fatalf("err = %v, want the op validation", err)
	}
	watches, err := s.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 0 {
		t.Fatalf("watches = %+v, want the valid row rolled back too", watches)
	}
}

func TestWatchStatusCarriesColorIdentity(t *testing.T) {
	s := newTestStore(t)
	swamp := scryfall.Card{ID: "swp", Set: "c21", CollectorNumber: "300",
		Name: "Swamp", PriceUSD: f(1), Raw: []byte(`{"color_identity":["B"]}`)}
	sol := scryfall.Card{ID: "sol", Set: "c21", CollectorNumber: "125",
		Name: "Sol Ring", PriceUSD: f(2), Raw: []byte(`{"color_identity":[]}`)}

	unfetched := scryfall.Card{ID: "unf", Set: "xxx", CollectorNumber: "1",
		Name: "Unfetched Card", PriceUSD: f(3)}
	for _, c := range []scryfall.Card{swamp, sol, unfetched} {
		if err := s.AddCardFinish(c, finish.Nonfoil, 1); err != nil {
			t.Fatalf("AddCardFinish %s: %v", c.Name, err)
		}
		if err := s.AddWatch(c.ID, c.Name, finish.Nonfoil, "over", 0.01); err != nil {
			t.Fatalf("AddWatch %s: %v", c.Name, err)
		}
	}

	watches, err := s.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	got := map[string][]string{}
	nilFor := map[string]bool{}
	for _, w := range watches {
		got[w.Name] = w.ColorIdentity
		nilFor[w.Name] = w.ColorIdentity == nil
	}
	if want := []string{"B"}; !slices.Equal(got["Swamp"], want) {
		t.Errorf("Swamp ColorIdentity = %v, want %v", got["Swamp"], want)
	}
	if len(got["Sol Ring"]) != 0 || nilFor["Sol Ring"] {
		t.Errorf("a colorless card's ColorIdentity = %v (nil %v), want an empty non-nil slice",
			got["Sol Ring"], nilFor["Sol Ring"])
	}
	if !nilFor["Unfetched Card"] {
		t.Errorf("an unfetched printing's ColorIdentity = %v, want nil", got["Unfetched Card"])
	}
}

func TestWatchAbsoluteLatchesUntilThePriceCrossesBack(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Fatalf("first check fired %d, want 1", len(fired))
	}
	for i := range 2 {
		if fired, _ := checkWatches(t, s); len(fired) != 0 {
			t.Fatalf("check %d fired %d alerts against an unchanged price", i+2, len(fired))
		}
	}

	w, err := s.ListWatches()
	if err != nil || len(w) != 1 {
		t.Fatalf("ListWatches = %+v, %v", w, err)
	}
	if w[0].State() != "met" {
		t.Errorf("state = %q after three checks, want it still met", w[0].State())
	}
	if w[0].WouldFire() {
		t.Error("wouldFire is true on a watch already reported")
	}
	if w[0].LastState != "met" {
		t.Errorf("last_state = %q, want met — the latch", w[0].LastState)
	}

	reprice(t, s, 15)
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("leaving the threshold fired an alert")
	}
	if w, _ := s.ListWatches(); w[0].LastState != "unmet" {
		t.Errorf("last_state = %q after the price left, want unmet", w[0].LastState)
	}
	reprice(t, s, 11)
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Error("re-crossing did not fire: nothing re-arms the watch")
	}
}

func TestWatchReAddReArmsALatchedWatch(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Fatal("setup: the first check should fire")
	}
	if fired, _ := checkWatches(t, s); len(fired) != 0 {
		t.Fatal("setup: the second check should be quiet")
	}

	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if fired, _ := checkWatches(t, s); len(fired) != 1 {
		t.Error("re-adding the same watch did not re-arm it")
	}
}
