package action

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func backfillStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.AddCardFinish(scryfall.Card{
		ID: "abc", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
		Finishes: []string{"nonfoil"},
	}, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	return st
}

// A second backfill the same day against the same holdings skips before the
// 150 MB download: the ledger receipt is checked ahead of any network work,
// so this test needs no server — reaching for one would fail it.
func TestBackfillSkipsSameDayRerun(t *testing.T) {
	st := backfillStore(t)
	owned, err := st.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		t.Fatalf("owned: %v %d", err, len(owned))
	}
	if err := st.RecordReceipt(store.ImportReceipt{
		Hash: backfillKey(owned), File: "backfill test", Cards: 1,
	}); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	res, err := BackfillPrices(context.Background(),
		Deps{Store: st, CacheDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if res.AlreadyToday == "" {
		t.Fatal("same-day re-run must skip via the ledger receipt")
	}
	if res.Printings == 0 {
		t.Error("the skip should still report what it covers")
	}
}

// The key binds to the holdings: adding a card changes it, so the skip can
// never hide a printing that has no history yet.
func TestBackfillKeyChangesWithHoldings(t *testing.T) {
	st := backfillStore(t)
	owned, _ := st.OwnedByFinish()
	before := backfillKey(owned)

	if err := st.AddCardFinish(scryfall.Card{
		ID: "def", Name: "Ancient Tomb", Set: "uma", CollectorNumber: "236",
		Finishes: []string{"nonfoil"},
	}, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	owned, _ = st.OwnedByFinish()
	after := backfillKey(owned)
	if before == after {
		t.Fatal("key must change when holdings change")
	}
}
