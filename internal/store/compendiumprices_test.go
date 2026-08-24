package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func snapshotCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM value_snapshots`).Scan(&n); err != nil {
		t.Fatalf("counting snapshots: %v", err)
	}
	return n
}

func TestRecordPricesTreatsACompendiumLikeAnyOtherDatabase(t *testing.T) {
	for _, compendium := range []bool{false, true} {
		s := newTestStore(t)
		if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
			t.Fatalf("AddCardFinish: %v", err)
		}
		if compendium {
			if err := s.SetCompendiumMode(true); err != nil {
				t.Fatalf("SetCompendiumMode: %v", err)
			}
		}

		if _, err := s.RecordPrices(); err != nil {
			t.Fatalf("RecordPrices (compendium=%v): %v", compendium, err)
		}
		if n := observationCount(t, s); n == 0 {
			t.Errorf("compendium=%v: no price history recorded", compendium)
		}
		if n := snapshotCount(t, s); n != 1 {
			t.Errorf("compendium=%v: recorded %d value snapshots, want 1 — "+
				"the stamp must not change what gets written", compendium, n)
		}
	}
}
