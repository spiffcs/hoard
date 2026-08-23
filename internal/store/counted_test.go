package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

// wantsStore holds Ulamog ($10) in the default binder and Sol Ring ($2) in a
// "Want" binder that is not counted toward the collection.
func wantsStore(t *testing.T) (s *Store, want int64) {
	t.Helper()
	s = newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	want, err := s.CreateBinder("Want")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(want, solRing(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := s.SetBinderCounted(want, false); err != nil {
		t.Fatalf("SetBinderCounted: %v", err)
	}
	return s, want
}

func TestBinderCountedRoundTrips(t *testing.T) {
	s, want := wantsStore(t)

	binders, err := s.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	for _, b := range binders {
		wantCounted := b.ID != want
		if b.Counted != wantCounted {
			t.Errorf("binder %q counted = %v, want %v", b.Name, b.Counted, wantCounted)
		}
	}

	if err := s.SetBinderCounted(want, true); err != nil {
		t.Fatalf("SetBinderCounted(true): %v", err)
	}
	binders, _ = s.ListBinders()
	for _, b := range binders {
		if !b.Counted {
			t.Errorf("binder %q is still uncounted after being switched back", b.Name)
		}
	}
}

func TestUncountedBinderIsLeftOutOfTheCollectionTotals(t *testing.T) {
	s, _ := wantsStore(t)

	tot, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if tot.TotalCopies != 2 || tot.DistinctCards != 1 {
		t.Errorf("totals = %d copies of %d cards, want only the 2 Ulamogs",
			tot.TotalCopies, tot.DistinctCards)
	}
	if tot.Value != 20 {
		t.Errorf("value = %v, want 20 — the Want binder must not inflate it", tot.Value)
	}
}

func TestUncountedBinderIsLeftOutOfAllCardsAndSets(t *testing.T) {
	s, _ := wantsStore(t)

	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Ulamog, the Infinite Gyre" {
		t.Errorf("AllByFinish = %v, want only the owned card", collNames(rows))
	}

	sets, err := s.SetsHeld()
	if err != nil {
		t.Fatalf("SetsHeld: %v", err)
	}
	var codes []string
	for _, st := range sets {
		codes = append(codes, st.Code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"uma"}) {
		t.Errorf("SetsHeld = %v, want only uma — c21 is wanted, not held", codes)
	}
}

func TestUncountedCardsAreStillPricedButCountZero(t *testing.T) {
	s, _ := wantsStore(t)

	ids, err := s.ActivePrintingIDs()
	if err != nil {
		t.Fatalf("ActivePrintingIDs: %v", err)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"sol-id", "ulamog-id"}) {
		t.Errorf("ActivePrintingIDs = %v, want both — a want still needs prices", ids)
	}

	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	got := map[string]int{}
	value := map[string]float64{}
	for _, o := range owned {
		got[o.ScryfallID] = o.Copies
		value[o.ScryfallID] = o.Value
	}
	if len(got) != 2 {
		t.Fatalf("OwnedByFinish = %v, want both printings so the backfill prices them", got)
	}
	if got["ulamog-id"] != 2 {
		t.Errorf("owned copies = %d, want 2", got["ulamog-id"])
	}
	if got["sol-id"] != 0 || value["sol-id"] != 0 {
		t.Errorf("wanted card reports %d copies worth %v, want 0 of each",
			got["sol-id"], value["sol-id"])
	}
}

func collNames(rows []CollectionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
