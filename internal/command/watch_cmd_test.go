package command

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
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
	// The write subcommands still reject --json rather than printing prose at
	// a script. `list` no longer does — it is a reader, and refusing it was
	// what left watch state unreachable to anything but a human.
	for _, args := range [][]string{
		{"add", "Sol Ring", "--under", "2"},
		{"rm", "sol"},
	} {
		if err := execWatch(ctx, st, args, true); err == nil {
			t.Errorf("watch %v --json succeeded, want an error", args)
		}
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

// bitterblossom is the demo's shape: the owner holds uma/85, and Scryfall's
// answer to the bare name is 2x2/69. The stub indexes by name last-wins, the
// same way /cards/collection answers a name identifier with one printing of
// its own choosing, so registering 2x2 last reproduces the substitution
// exactly.
func bitterblossom() (held, other scryfall.Card) {
	held = scryfall.Card{ID: "bb-uma", Set: "uma", CollectorNumber: "85",
		Name: "Bitterblossom", ScryfallURL: "http://uma", PriceUSD: f(34.80),
		PriceUSDFoil: f(60), Finishes: []string{"nonfoil", "foil"}}
	other = scryfall.Card{ID: "bb-2x2", Set: "2x2", CollectorNumber: "69",
		Name: "Bitterblossom", ScryfallURL: "http://2x2", PriceUSD: f(32.97),
		PriceUSDFoil: f(55), Finishes: []string{"nonfoil", "foil"}}
	return held, other
}

// holdBitterblossom puts qty copies of the uma printing in the collection and
// arms the stub with both printings, 2x2 last.
func holdBitterblossom(t *testing.T, st *store.Store, finish string, qty int) (held, other scryfall.Card) {
	t.Helper()
	held, other = bitterblossom()
	if err := st.AddCardFinish(held, finish, qty); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	stubFetch(t, held, other)
	return held, other
}

// FINDING #1, the highest-severity one: a watch added by bare name must land
// on a printing the collection actually holds.
//
// The failure it replaces was silent — "Watching Bitterblossom (2x2/69)
// nonfoil: over $1.00." reads as a success — so the assertion is on the id in
// the store, not on the prose.
func TestCmdWatchAddPrefersTheHeldPrinting(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, "nonfoil", 4)

	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--over", "1"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 1 {
		t.Fatalf("watches = %+v, %v, want exactly one", watches, err)
	}
	if w := watches[0]; w.ScryfallID != "bb-uma" {
		t.Errorf("watch stood on %s (%s/%s), want the held bb-uma (uma/85)",
			w.ScryfallID, w.SetCode, w.CollectorNumber)
	}
	if !strings.Contains(out, "(uma/85)") {
		t.Errorf("confirmation = %q, want it to name uma/85", out)
	}
	// Nothing to disclose: one held printing, and it is the one watched.
	if strings.Contains(out, "You hold") || strings.Contains(out, "Also held") {
		t.Errorf("confirmation = %q, want no disambiguation note for the unambiguous case", out)
	}
}

// The name is typed by a person or a script and does not have to match the
// catalog's capitalisation. An agent that lowercases its inputs must not fall
// silently through to Scryfall's pick.
func TestCmdWatchAddPrefersHeldPrintingCaseInsensitively(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, "nonfoil", 4)
	if err := execWatch(context.Background(), st,
		[]string{"add", "bitterblossom", "--over", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	w, _ := st.ListWatches()
	if len(w) != 1 || w[0].ScryfallID != "bb-uma" {
		t.Errorf("watches = %+v, want the held uma printing", w)
	}
}

// The other half of the fix: when the card is genuinely not held, the old
// behaviour stands — a watch on a card you hope to buy is the --under case
// entirely — but the substitution is stated instead of hidden.
func TestCmdWatchAddSaysWhenNoPrintingIsHeld(t *testing.T) {
	st := exportStore(t)
	_, other := bitterblossom()
	held, _ := bitterblossom()
	stubFetch(t, held, other) // nothing added to the collection

	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--under", "20"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
		t.Errorf("watches = %+v, want Scryfall's pick to still stand", w)
	}
	if !strings.Contains(out, "You hold no copy of Bitterblossom") {
		t.Errorf("confirmation = %q, want it to say the printing is not one of yours", out)
	}
}

// Several held printings: the tie-break is copies, and the ones passed over
// are named. Quietly picking among them would be the same failure as quietly
// picking a printing nobody owns, one size smaller.
func TestCmdWatchAddNamesTheOtherHeldPrintings(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish uma: %v", err)
	}
	if err := st.AddCardFinish(other, "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinish 2x2: %v", err)
	}
	stubFetch(t, held, other)

	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--over", "1"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}
	// Three copies of 2x2/69 against one of uma/85: most copies wins.
	if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
		t.Fatalf("watches = %+v, want the printing held in the greater number", w)
	}
	if !strings.Contains(out, "You hold 2 printings of Bitterblossom; watching the one you hold most of (\u00d73)") {
		t.Errorf("confirmation = %q, want the count of held printings", out)
	}
	if !strings.Contains(out, "Also held: uma/85 ×1") {
		t.Errorf("confirmation = %q, want the passed-over printing named with its count", out)
	}
}

// Determinism, which is what an agent standing the same watch twice depends
// on: equal copies must still resolve the same way every run rather than by
// whatever order the rows came back in.
func TestCmdWatchAddTieBreakIsStable(t *testing.T) {
	for i := range 5 {
		st := exportStore(t)
		held, other := bitterblossom()
		if err := st.AddCardFinish(other, "nonfoil", 2); err != nil {
			t.Fatalf("AddCardFinish 2x2: %v", err)
		}
		if err := st.AddCardFinish(held, "nonfoil", 2); err != nil {
			t.Fatalf("AddCardFinish uma: %v", err)
		}
		// uma registered last, so the stub's name index — Scryfall's pick —
		// answers uma. The tie-break has to answer 2x2 anyway, or this test
		// passes on the unfixed code and proves nothing.
		stubFetch(t, other, held)
		if err := execWatch(context.Background(), st,
			[]string{"add", "Bitterblossom", "--over", "1"}, false); err != nil {
			t.Fatalf("watch add: %v", err)
		}
		// Equal copies, so set code decides: "2x2" sorts before "uma".
		if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
			t.Fatalf("run %d: watches = %+v, want the same printing every run", i, w)
		}
	}
}

// A foil watch prefers a printing held in foil. Four non-foil 2x2 copies
// against one foil uma copy: the finish, not the copy count, has to decide, or
// a --foil watch lands on the printing whose foil nobody owns. The stub
// answers 2x2 for the bare name, so this fails on the unfixed code too.
func TestCmdWatchAddFoilPrefersThePrintingHeldInFoil(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, "foil", 1); err != nil {
		t.Fatalf("AddCardFinish uma foil: %v", err)
	}
	if err := st.AddCardFinish(other, "nonfoil", 4); err != nil {
		t.Fatalf("AddCardFinish 2x2 nonfoil: %v", err)
	}
	stubFetch(t, held, other)

	if err := execWatch(context.Background(), st,
		[]string{"add", "Bitterblossom", "--foil", "--over", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	w, _ := st.ListWatches()
	if len(w) != 1 || w[0].ScryfallID != "bb-uma" || w[0].Finish != "foil" {
		t.Errorf("watches = %+v, want the foil-held uma printing", w)
	}
}

// Held in one finish, watched in the other: still your printing, and the note
// says which part of it you do not own.
func TestCmdWatchAddSaysWhenTheFinishIsNotHeld(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, "nonfoil", 4)
	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--foil", "--over", "1"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-uma" {
		t.Fatalf("watches = %+v, want the held printing even for a finish not held", w)
	}
	if !strings.Contains(out, "no copy of it is foil") {
		t.Errorf("confirmation = %q, want it to say the foil is not held", out)
	}
}

// Preferring a held printing must not cost a second round trip: the id goes in
// where the name went, and that is one /cards/collection call as before.
func TestCmdWatchAddPreferHeldResolvesOnce(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, "nonfoil", 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	calls := stubFetch(t, held, other)
	if err := execWatch(context.Background(), st,
		[]string{"add", "Bitterblossom", "--over", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if *calls != 1 {
		t.Errorf("resolved %d times, want 1", *calls)
	}
}

// The fallback resolve is load-bearing, not decoration: a stored id Scryfall
// no longer knows must not turn "watch this card" into "no card matches".
// resolve's existing name retry catches it, and the note says the watch is on
// a printing that is not one of yours.
func TestCmdWatchAddFallsBackWhenTheHeldIDIsUnknown(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, "nonfoil", 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	// Only the 2x2 printing is answerable; bb-uma is a dead id.
	stubFetch(t, other)

	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--over", "1"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}
	if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
		t.Fatalf("watches = %+v, want the name retry's answer", w)
	}
	if !strings.Contains(out, "did not answer for the printing you hold (uma/85") {
		t.Errorf("confirmation = %q, want it to report the substitution", out)
	}
}

// FINDING #3: watch state has to be readable by a machine. `hoard watch
// --json` says only what one check did; without this, an agent that missed an
// exit 3 could never ask what is currently met, because firing latches and the
// event is gone.
func TestCmdWatchListJSON(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	// $2 nonfoil under $5: met from the first check. $12.50 foil over $50:
	// waiting. Two rows, two states, one document.
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--under", "5"}, false); err != nil {
		t.Fatalf("watch add under: %v", err)
	}
	if err := execWatch(ctx, st, []string{"add", "Sol Ring", "--foil", "--over", "50"}, false); err != nil {
		t.Fatalf("watch add over: %v", err)
	}

	out, err := execCmd(ctx, st, []string{"watch", "list"}, true)
	if err != nil {
		t.Fatalf("watch list --json: %v", err)
	}
	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Kind          string `json:"kind"`
		Watches       struct {
			Rows []struct {
				ID   int64 `json:"id"`
				Card struct {
					Name    string `json:"name"`
					SetCode string `json:"setCode"`
					Finish  string `json:"finish"`
				} `json:"card"`
				Op           string   `json:"op"`
				ThresholdUsd float64  `json:"thresholdUsd"`
				PriceUsd     *float64 `json:"priceUsd"`
				State        string   `json:"state"`
				WouldFire    bool     `json:"wouldFire"`
				LastFiredAt  string   `json:"lastFiredAt"`
			} `json:"rows"`
		} `json:"watches"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	if doc.Kind != "watches" || doc.SchemaVersion == "" {
		t.Errorf("envelope = %s/%s, want the watches kind with a version", doc.Kind, doc.SchemaVersion)
	}
	if len(doc.Watches.Rows) != 2 {
		t.Fatalf("rows = %+v, want 2", doc.Watches.Rows)
	}
	byOp := map[string]int{}
	for i, r := range doc.Watches.Rows {
		byOp[r.Op] = i
		if r.ID == 0 {
			t.Errorf("row %d has no id; watch rm takes one", i)
		}
		if r.Card.Name != "Sol Ring" {
			t.Errorf("row %d card = %+v", i, r.Card)
		}
	}
	under := doc.Watches.Rows[byOp["under"]]
	if under.State != "met" || !under.WouldFire {
		t.Errorf("under row = %+v, want met and about to fire", under)
	}
	if under.PriceUsd == nil || *under.PriceUsd != 2 {
		t.Errorf("under row price = %v, want 2", under.PriceUsd)
	}
	over := doc.Watches.Rows[byOp["over"]]
	if over.State != "waiting" || over.WouldFire {
		t.Errorf("over row = %+v, want waiting and quiet", over)
	}

	// After the check, the crossing is spent: still met, no longer about to
	// fire, and dated. This is the pair of fields finding #4 is about.
	if err := execWatch(ctx, st, nil, false); !errors.Is(err, errWatchFired) {
		t.Fatalf("check = %v, want errWatchFired", err)
	}
	out, err = execCmd(ctx, st, []string{"watch", "list"}, true)
	if err != nil {
		t.Fatalf("watch list --json: %v", err)
	}
	doc.Watches.Rows = nil
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	under = doc.Watches.Rows[byOp["under"]]
	if under.State != "met" {
		t.Errorf("under row state = %q after firing, want it still met", under.State)
	}
	if under.WouldFire {
		t.Error("under row would fire again after being reported: the latch is invisible")
	}
	if under.LastFiredAt == "" {
		t.Error("under row has no lastFiredAt after firing")
	}
}

// An empty collection of watches is an answer, not an error, and it has to
// arrive in the same shape.
func TestCmdWatchListJSONEmpty(t *testing.T) {
	st := exportStore(t)
	out, err := execCmd(context.Background(), st, []string{"watch", "list"}, true)
	if err != nil {
		t.Fatalf("watch list --json: %v", err)
	}
	var doc struct {
		Kind    string `json:"kind"`
		Watches struct {
			Rows []any `json:"rows"`
		} `json:"watches"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	if doc.Kind != "watches" || doc.Watches.Rows == nil || len(doc.Watches.Rows) != 0 {
		t.Errorf("empty listing = %q, want a watches document with an empty rows array", out)
	}
}

// The kind has to be reachable from `hoard schema --kind`, which this project
// treats as the LLM-integration contract: a kind the schema command cannot
// narrow to is only half delivered.
func TestCmdSchemaKindWatches(t *testing.T) {
	st := exportStore(t)
	out, err := execCmd(context.Background(), st, []string{"schema", "--kind", "watches"}, false)
	if err != nil {
		t.Fatalf("hoard schema --kind watches: %v", err)
	}
	for _, want := range []string{`"watches"`, `"wouldFire"`, "waiting-on-history"} {
		if !strings.Contains(out, want) {
			t.Errorf("sliced schema does not mention %s", want)
		}
	}
}

// FINDING #4: the latch is good behaviour that was undiscoverable. `hoard
// watch --help` said "exit 3 = fired" and stopped, so the obvious supervisor
// loop reads exit 0 as "no longer crossed". The help now states the latch and
// both re-arm rules; this pins the text to the behaviour the store tests
// demonstrate, so the two cannot drift apart silently.
func TestCmdWatchHelpDocumentsTheLatch(t *testing.T) {
	var b strings.Builder
	renderHelp(&b, ui.Env{Width: 80}, "watch")
	help := b.String()
	for _, want := range []string{
		// The latch itself, and that exit 0 does not mean "not crossed".
		"fires once",
		"Exit 0",
		// The escape hatch a machine has.
		"hoard watch list --json",
		// Both re-arm rules, which differ by op: an absolute watch waits for
		// the price to leave, a movement re-arms on the firing alone.
		"outside the threshold",
		"re-adding",
		"re-anchors",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("hoard watch --help does not mention %q:\n%s", want, help)
		}
	}
}
