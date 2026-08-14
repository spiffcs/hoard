package command

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/spiffcs/hoard/internal/finish"
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
	if w := watches[0]; w.ScryfallID != "sol" || w.Finish != finish.Nonfoil ||
		w.Op != "under" || w.Threshold != 5 {
		t.Errorf("watch = %+v", w)
	}

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
		{"add", "Sol Ring"},
		{"add", "--under", "2"},
	} {
		if err := execWatch(ctx, st, args, false); err == nil {
			t.Errorf("execWatch(%v) succeeded, want an error", args)
		}
	}

	for _, args := range [][]string{
		{"add", "Sol Ring", "--under", "2"},
		{"rm", "sol"},
	} {
		if err := execWatch(ctx, st, args, true); err == nil {
			t.Errorf("watch %v --json succeeded, want an error", args)
		}
	}
}

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

	if !strings.Contains(out, "under $1.00, over $5.00") {
		t.Errorf("confirmation = %q, want both bounds on one line", out)
	}
	if !strings.Contains(out, "Two watches") {
		t.Errorf("confirmation = %q, want it to say the band is two watches", out)
	}
}

func TestCmdWatchAddRejectsReversedBand(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	for _, args := range [][]string{
		{"add", "Sol Ring", "--under", "5", "--over", "1"},
		{"add", "Sol Ring", "--under", "5", "--over", "5"},
	} {
		err := execWatch(ctx, st, args, false)
		if err == nil {
			t.Fatalf("execWatch(%v) succeeded, want a usage error", args)
		}

		if !strings.Contains(err.Error(), "every price") {
			t.Errorf("err = %v, want it to explain that the band matches every price", err)
		}
		if w, _ := st.ListWatches(); len(w) != 0 {
			t.Fatalf("watches = %+v, want nothing stood by a refused band", w)
		}
	}
}

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
	if w := fired[0]; w.Finish != finish.Foil || *w.PriceUSD != 12.5 {
		t.Errorf("fired = %+v, want the foil price", w)
	}
}

func TestCmdWatchAddUnknownCard(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	err := execWatch(context.Background(), st, []string{"add", "No Such Card", "--under", "5"}, false)
	if err == nil || !strings.Contains(err.Error(), "No Such Card") {
		t.Errorf("err = %v, want a no-match error naming the card", err)
	}
}

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

	if w, err := st.ListWatches(); err != nil || len(w) != 2 {
		t.Errorf("watches = %+v, %v, want 2", w, err)
	}
}

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

func bitterblossom() (held, other scryfall.Card) {
	held = scryfall.Card{ID: "bb-uma", Set: "uma", CollectorNumber: "85",
		Name: "Bitterblossom", ScryfallURL: "http://uma", PriceUSD: f(34.80),
		PriceUSDFoil: f(60), Finishes: []string{"nonfoil", "foil"}}
	other = scryfall.Card{ID: "bb-2x2", Set: "2x2", CollectorNumber: "69",
		Name: "Bitterblossom", ScryfallURL: "http://2x2", PriceUSD: f(32.97),
		PriceUSDFoil: f(55), Finishes: []string{"nonfoil", "foil"}}
	return held, other
}

func holdBitterblossom(t *testing.T, st *store.Store, fin finish.Finish, qty int) (held, other scryfall.Card) {
	t.Helper()
	held, other = bitterblossom()
	if err := st.AddCardFinish(held, fin, qty); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	stubFetch(t, held, other)
	return held, other
}

func TestCmdWatchAddPrefersTheHeldPrinting(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, finish.Nonfoil, 4)

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

	if strings.Contains(out, "You hold") || strings.Contains(out, "Also held") {
		t.Errorf("confirmation = %q, want no disambiguation note for the unambiguous case", out)
	}
}

func TestCmdWatchAddPrefersHeldPrintingCaseInsensitively(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, finish.Nonfoil, 4)
	if err := execWatch(context.Background(), st,
		[]string{"add", "bitterblossom", "--over", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	w, _ := st.ListWatches()
	if len(w) != 1 || w[0].ScryfallID != "bb-uma" {
		t.Errorf("watches = %+v, want the held uma printing", w)
	}
}

func TestCmdWatchAddSaysWhenNoPrintingIsHeld(t *testing.T) {
	st := exportStore(t)
	_, other := bitterblossom()
	held, _ := bitterblossom()
	stubFetch(t, held, other)

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

func TestCmdWatchAddNamesTheOtherHeldPrintings(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish uma: %v", err)
	}
	if err := st.AddCardFinish(other, finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinish 2x2: %v", err)
	}
	stubFetch(t, held, other)

	out, err := execCmd(context.Background(), st,
		[]string{"watch", "add", "Bitterblossom", "--over", "1"}, false)
	if err != nil {
		t.Fatalf("watch add: %v", err)
	}

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

func TestCmdWatchAddTieBreakIsStable(t *testing.T) {
	for i := range 5 {
		st := exportStore(t)
		held, other := bitterblossom()
		if err := st.AddCardFinish(other, finish.Nonfoil, 2); err != nil {
			t.Fatalf("AddCardFinish 2x2: %v", err)
		}
		if err := st.AddCardFinish(held, finish.Nonfoil, 2); err != nil {
			t.Fatalf("AddCardFinish uma: %v", err)
		}

		stubFetch(t, other, held)
		if err := execWatch(context.Background(), st,
			[]string{"add", "Bitterblossom", "--over", "1"}, false); err != nil {
			t.Fatalf("watch add: %v", err)
		}

		if w, _ := st.ListWatches(); len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
			t.Fatalf("run %d: watches = %+v, want the same printing every run", i, w)
		}
	}
}

func TestCmdWatchAddFoilPrefersThePrintingHeldInFoil(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish uma foil: %v", err)
	}
	if err := st.AddCardFinish(other, finish.Nonfoil, 4); err != nil {
		t.Fatalf("AddCardFinish 2x2 nonfoil: %v", err)
	}
	stubFetch(t, held, other)

	if err := execWatch(context.Background(), st,
		[]string{"add", "Bitterblossom", "--foil", "--over", "1"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	w, _ := st.ListWatches()
	if len(w) != 1 || w[0].ScryfallID != "bb-uma" || w[0].Finish != finish.Foil {
		t.Errorf("watches = %+v, want the foil-held uma printing", w)
	}
}

func TestCmdWatchAddSaysWhenTheFinishIsNotHeld(t *testing.T) {
	st := exportStore(t)
	holdBitterblossom(t, st, finish.Nonfoil, 4)
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

func TestCmdWatchAddPreferHeldResolvesOnce(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Nonfoil, 4); err != nil {
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

func TestCmdWatchAddFallsBackWhenTheHeldIDIsUnknown(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Nonfoil, 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

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

func TestCmdWatchListJSON(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

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

func TestCmdWatchHelpDocumentsTheLatch(t *testing.T) {
	var b strings.Builder
	renderHelp(&b, ui.Env{Width: 80}, "watch")
	help := b.String()
	for _, want := range []string{

		"fires once",
		"Exit 0",

		"hoard watch list --json",

		"outside the threshold",
		"re-adding",
		"re-anchors",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("hoard watch --help does not mention %q:\n%s", want, help)
		}
	}
}
