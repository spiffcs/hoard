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

func TestRecordPricesWritesAValueSnapshot(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if n := snapshotCount(t, s); n != 1 {
		t.Errorf("recorded %d value snapshots, want 1", n)
	}
}
