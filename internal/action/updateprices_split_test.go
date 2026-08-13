package action

// The split that lets the browser show refreshed prices before paying for the
// slowest question a price update asks.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func splitStore(t *testing.T) (*store.Store, Deps) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	card := scryfall.Card{ID: "sol", Set: "lea", CollectorNumber: "1",
		Name: "Sol Ring", ScryfallURL: "http://x", PriceUSD: f(12)}
	if err := st.AddCardFinish(card, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	return st, Deps{
		Store:   st,
		Confirm: func(string) bool { return false },
		Resolver: &resolve.Resolver{Fetch: func(_ context.Context,
			ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
			return []scryfall.Card{card}, nil, nil
		}},
	}
}

// The reorder this file guards: history is written by the refresh half, so the
// browser's first redraw already carries today's observation and movers has
// something to chart. It used to wait behind the correction sweep — twenty
// seconds of paced vendor requests that change nothing on almost every run.
func TestRefreshPricesRecordsHistoryImmediately(t *testing.T) {
	st, deps := splitStore(t)

	res, err := RefreshPrices(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("RefreshPrices: %v", err)
	}
	if res.Found != 1 {
		t.Fatalf("result = %+v, want the printing refreshed", res)
	}
	obs, _, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if obs == 0 {
		t.Fatal("the refresh half recorded no history; movers would open empty until the sweep landed")
	}

	// The deferred half repairs rather than records, and carries the refresh's
	// counts through so a caller ends up with one filled-in result either way.
	before := obs
	res, err = CorrectPrices(context.Background(), deps, nil, res)
	if err != nil {
		t.Fatalf("CorrectPrices: %v", err)
	}
	if res.Found != 1 || res.Total != 1 {
		t.Errorf("result = %+v, want the refresh's counts carried through", res)
	}
	if res.Repaired != 0 {
		t.Errorf("Repaired = %d on a run with nothing to refuse, want 0", res.Repaired)
	}
	obs, _, err = st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if obs != before {
		t.Errorf("history went from %d to %d observations; a quiet sweep must not touch it",
			before, obs)
	}
}

// The composed call is what every batch caller still uses, and it must behave
// as it always did: both halves, in order, one result.
func TestUpdatePricesStillRecordsInOneCall(t *testing.T) {
	st, deps := splitStore(t)

	res, err := UpdatePrices(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if res.Total != 1 || res.Found != 1 {
		t.Errorf("result = %+v, want 1 of 1 found", res)
	}
	obs, _, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if obs == 0 {
		t.Error("the composed call recorded no history")
	}
}

// An empty hoard short-circuits before either half does any work; the composed
// call must not fall through into a correction sweep over nothing.
func TestUpdatePricesOnAnEmptyHoardDoesNothing(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	res, err := UpdatePrices(context.Background(), Deps{Store: st}, nil)
	if err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if res.Total != 0 || res.Found != 0 || res.Refused != 0 {
		t.Errorf("result = %+v, want an untouched zero result", res)
	}
}
