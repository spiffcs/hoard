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
		{"add", "Sol Ring"},     // no threshold at all
		{"add", "--under", "2"}, // no name
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

// Both bounds at once is a band: alert outside $1–$5. The store has always
// held it — its key is (card, finish, op), so the two directions are two
// rows — and the CLI was the only thing forbidding it.
func TestCmdWatchAddBand(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Sol Ring", "--under", "1", "--over", "5"}, false)
	if err != nil {
		t.Fatalf("band add: %v", err)
	}

	watches, err := st.ListWatches()
	if err != nil || len(watches) != 2 {
		t.Fatalf("watches = %+v, %v, want 2", watches, err)
	}
	byOp := map[string]float64{}
	ids := map[int64]bool{}
	for _, w := range watches {
		byOp[w.Op] = w.Threshold
		ids[int64(w.ID)] = true
	}
	if byOp["under"] != 1 || byOp["over"] != 5 {
		t.Errorf("thresholds = %+v, want under 1 and over 5", byOp)
	}
	if len(ids) != 2 {
		t.Errorf("ids = %v, want two distinct watches", ids)
	}
	// One command, one confirmation: both bounds on the line, and a word
	// about the two rows, because rm and list deal in one direction each.
	if !strings.Contains(out, "under $1.00, over $5.00") {
		t.Errorf("confirmation = %q, want both bounds on one line", out)
	}
	if !strings.Contains(out, "Two watches") {
		t.Errorf("confirmation = %q, want it to say the band is two watches", out)
	}
}

// The control that matters: --under 5 --over 1 is not a band, it is every
// price in the world. Nothing downstream can catch it — both rows are
// individually valid and each will fire — so it has to die at the flag.
func TestCmdWatchAddRejectsReversedBand(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	for _, args := range [][]string{
		{"add", "Sol Ring", "--under", "5", "--over", "1"}, // fires on everything
		{"add", "Sol Ring", "--under", "5", "--over", "5"}, // fires on all but exactly $5
	} {
		err := execWatch(ctx, st, args, false)
		if err == nil {
			t.Fatalf("execWatch(%v) succeeded, want a usage error", args)
		}
		// The refusal has to say why, or it reads as an arbitrary rule and
		// the user simply runs the two commands separately.
		if !strings.Contains(err.Error(), "every price") {
			t.Errorf("err = %v, want it to explain that the band matches every price", err)
		}
		if w, _ := st.ListWatches(); len(w) != 0 {
			t.Fatalf("watches = %+v, want nothing stood by a refused band", w)
		}
	}
}

// Re-running one direction adjusts that direction and leaves the other
// standing. Add must never be a silent remove: the upsert keys on op, and a
// bare --under that cleared the --over would delete a watch nobody named.
func TestCmdWatchAddBandAdjustsOneDirection(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--under", "1", "--over", "5"}, false); err != nil {
		t.Fatalf("band add: %v", err)
	}
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--under", "2"}, false); err != nil {
		t.Fatalf("re-add one direction: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 2 {
		t.Fatalf("watches = %+v, %v, want still 2", watches, err)
	}
	for _, w := range watches {
		switch w.Op {
		case "under":
			if w.Threshold != 2 {
				t.Errorf("under = %v, want the new 2", w.Threshold)
			}
		case "over":
			if w.Threshold != 5 {
				t.Errorf("over = %v, want the untouched 5", w.Threshold)
			}
		}
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

// A band is one card, so it is one resolve. The two directions were looped in
// the command, which named the card twice: two /cards/collection calls paced
// ~500ms apart by the shared limiter, for a question already answered. The
// control is the call count, not the output — the output was always right.
func TestCmdWatchAddBandResolvesOnce(t *testing.T) {
	st := exportStore(t)
	calls := stubFetch(t, watchCard())
	if _, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Sol Ring", "--under", "1", "--over", "5"}, false); err != nil {
		t.Fatalf("band add: %v", err)
	}
	if *calls != 1 {
		t.Errorf("resolved the card %d times, want 1", *calls)
	}
	// Still two rows: resolve once, write twice.
	if w, err := st.ListWatches(); err != nil || len(w) != 2 {
		t.Errorf("watches = %+v, %v, want 2", w, err)
	}
}

// One direction was always one resolve, and stays one.
func TestCmdWatchAddOneDirectionResolvesOnce(t *testing.T) {
	st := exportStore(t)
	calls := stubFetch(t, watchCard())
	if err := execWatch(context.Background(), st, []string{"add", "Sol Ring", "--under", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if *calls != 1 {
		t.Errorf("resolved the card %d times, want 1", *calls)
	}
}
