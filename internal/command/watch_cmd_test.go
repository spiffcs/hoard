package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func watchCard() scryfall.Card {
	return scryfall.Card{ID: "sol", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
		ScryfallURL: "http://x", PriceUSD: f(2), PriceUSDFoil: f(12.5),
		Finishes: []string{"nonfoil", "foil"}}
}

// The whole loop: add resolves once and pins the printing, the bare check
// fires on the crossing with exit-code sentinel, and a second check is quiet.
func TestCmdWatchAddCheckCycle(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	if err := execWatch(ctx, st, []string{"add", "Sol", "Ring", "--under", "5"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 1 {
		t.Fatalf("watches = %+v, %v", watches, err)
	}
	if w := watches[0]; w.ScryfallID != "sol" || w.Finish != "nonfoil" ||
		w.Op != "under" || w.Threshold != 5 {
		t.Errorf("watch = %+v", w)
	}

	// Price 2 is under 5: the first check alerts and signals exit 3.
	if err := execWatch(ctx, st, nil, false); !errors.Is(err, errWatchFired) {
		t.Fatalf("first check = %v, want errWatchFired", err)
	}
	if err := execWatch(ctx, st, nil, false); err != nil {
		t.Fatalf("second check = %v, want quiet success", err)
	}
}

func TestCmdWatchAddRejectsBadFlags(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	for _, args := range [][]string{
		{"add", "Sol Ring"}, // no threshold
		{"add", "Sol Ring", "--under", "2", "--over", "5"}, // both directions
		{"add", "--under", "2"},                            // no name
	} {
		if err := execWatch(ctx, st, args, false); err == nil {
			t.Errorf("execWatch(%v) succeeded, want an error", args)
		}
	}
	// Subcommands reject --json rather than printing a table at a script.
	if err := execWatch(ctx, st, []string{"list"}, true); err == nil {
		t.Error("watch list --json succeeded, want an error")
	}
}

func TestCmdWatchRemoveByFragment(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--over", "30"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if err := execWatch(ctx, st, []string{"rm", "sol"}, false); err != nil {
		t.Fatalf("watch rm: %v", err)
	}
	if watches, _ := st.ListWatches(); len(watches) != 0 {
		t.Errorf("watches after rm = %+v", watches)
	}
}

// A foil watch follows the foil price: the fixture's foil is $12.50, so an
// --under 20 foil watch fires while the $2 non-foil would not satisfy an
// over-10 reading by accident.
func TestCmdWatchFoil(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--foil", "--over", "10"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	fired, checked, err := st.CheckWatches()
	if err != nil || checked != 1 || len(fired) != 1 {
		t.Fatalf("check = %d fired of %d, %v", len(fired), checked, err)
	}
	if w := fired[0]; w.Finish != "foil" || *w.PriceUSD != 12.5 {
		t.Errorf("fired = %+v, want the foil price", w)
	}
}

// An unknown name must fail loudly at add time — a watch that can never fire
// because it pinned nothing is worse than an error.
func TestCmdWatchAddUnknownCard(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	err := execWatch(context.Background(), st, []string{"add", "No Such Card", "--under", "5"}, false)
	if err == nil || !strings.Contains(err.Error(), "No Such Card") {
		t.Errorf("err = %v, want a no-match error naming the card", err)
	}
}
