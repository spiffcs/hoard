package command

import (
	"context"
	"strings"
	"testing"

	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// seedSeries gives the fixture card a price history, through the same import
// path a real backfill uses — which writes as_of as an RFC 3339 midnight, the
// format the anchor's window bound compares against.
//
// The source is Scryfall because watchCard() carries a Scryfall price, so that
// is where its effective price comes from and therefore what the anchor reads.
func seedSeries(t *testing.T, st *store.Store, points map[string]float64) {
	t.Helper()
	obs := make([]mtgjson.Observation, 0, len(points))
	for date, price := range points {
		obs = append(obs, mtgjson.Observation{
			Date: date, Finish: "normal", Price: price, Source: "scryfall"})
	}
	if _, _, err := st.BackfillPrices(map[string][]mtgjson.Observation{"sol": obs}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}
}

// daysAgo is a backfill date N days before today. The command layer reads its
// own clock — only the store lets a test name the present — so a fixture here
// has to be placed relative to now or it walks out of its own window as the
// calendar moves.
func daysAgo(n int) string { return time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02") }

// plainEnv renders without styling, so an assertion reads the words rather
// than the escape codes around them.
func plainEnv() ui.Env { return ui.Env{Width: 80} }

// A bare number on --drop is a percentage, and the two ways a user gets that
// wrong are caught by name rather than stored.
func TestParsePercent(t *testing.T) {
	for _, tc := range []struct {
		flag, in string
		want     float64
		wantErr  string
	}{
		{flag: "drop", in: "10", want: 0.10},
		{flag: "drop", in: "10%", want: 0.10},
		{flag: "drop", in: " 12.5 % ", want: 0.125},
		{flag: "rise", in: "150", want: 1.50}, // a rise past doubling is a real question
		{flag: "drop", in: "0.10", wantErr: "a percentage, not a fraction"},
		{flag: "drop", in: "100", wantErr: "falling to nothing"},
		{flag: "drop", in: "abc", wantErr: "want a percentage"},
	} {
		got, err := parsePercent(tc.flag, tc.in)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("parsePercent(%q, %q) err = %v, want one naming %q",
					tc.flag, tc.in, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("parsePercent(%q, %q) = %v", tc.flag, tc.in, err)
		case got != tc.want:
			t.Errorf("parsePercent(%q, %q) = %v, want %v", tc.flag, tc.in, got, tc.want)
		}
	}
}

// A movement and a dollar line are two different questions about one printing,
// and asking both in one command would make watch rm ambiguous about which the
// user meant to drop.
func TestCmdWatchAddRefusesMixedUnits(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	err := execWatch(context.Background(), st,
		[]string{"add", "Sol", "Ring", "--drop", "10%", "--under", "2"}, false)
	if err == nil || !strings.Contains(err.Error(), "two different questions") {
		t.Fatalf("err = %v, want the mixed-units refusal", err)
	}
}

// A watch add stores the movement in its own units, and the row it writes
// cannot fire on a fall that predates it.
//
// The anchor's lower bound includes the watch's own creation, so a watch set
// today anchors on today's price however far the card has already fallen. That
// is the intended reading of "tell me when this moves" — the alternative is a
// watch that fires the moment it is created, reporting history as news.
func TestCmdWatchPercentAddDoesNotFireOnThePast(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	if err := execWatch(ctx, st, []string{"add", "Sol", "Ring", "--drop", "20%"}, false); err != nil {
		t.Fatalf("watch add --drop: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 1 {
		t.Fatalf("watches = %+v, %v", watches, err)
	}
	if w := watches[0]; w.Op != "drop" || w.Pct != 0.20 ||
		w.MinMove != store.DefaultMinMove || w.WindowDays != store.DefaultWindowDays {
		t.Fatalf("watch = %+v, want a drop carrying its own units", w)
	}
	// A dollar threshold must not have been written into the movement's row.
	if watches[0].Threshold != 0 {
		t.Errorf("threshold = %v on a movement, want it unused", watches[0].Threshold)
	}

	// A 33% fall, all of it before the watch existed.
	seedSeries(t, st, map[string]float64{daysAgo(200): 3.00, daysAgo(5): 2.00})
	if err := execWatch(ctx, st, nil, false); err != nil {
		t.Fatalf("the check reported a fall that predates the watch: %v", err)
	}
	w, _ := st.ListWatches()
	if w[0].Anchor == nil || *w[0].Anchor != 2.00 {
		t.Errorf("anchor = %v, want the price in effect when the watch was set", w[0].Anchor)
	}
}

// --since speaks movers' vocabulary, and lands in the store as whole days.
func TestCmdWatchAddSince(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	if err := execWatch(context.Background(), st,
		[]string{"add", "Sol", "Ring", "--rise", "10%", "--since", "2w", "--min-move", "5"},
		false); err != nil {
		t.Fatalf("watch add --since: %v", err)
	}
	watches, _ := st.ListWatches()
	if len(watches) != 1 || watches[0].WindowDays != 14 || watches[0].MinMove != 5 {
		t.Fatalf("watch = %+v, want a 14-day window and a $5 floor", watches)
	}
}

// The WATCH cell reads in the units the op names, and shows the floor only
// when it is doing something: a watch whose min-move is silently suppressing
// alerts has to say so on its own row, or "it moved 12%, why was I not told?"
// has no visible cause anywhere.
func TestWatchCellUnits(t *testing.T) {
	cell := func(op string, threshold, pct, minMove float64) string {
		return watchCell(store.WatchStatus{Watch: store.Watch{
			Op: op, Threshold: threshold, Pct: pct, MinMove: minMove}})
	}
	for _, tc := range []struct{ got, want string }{
		{cell("under", 12, 0, 0), "under $12.00"},
		{cell("drop", 0, 0.10, 0), "drop 10.0%"},
		{cell("drop", 0, 0.10, 5), "drop 10.0% ≥$5.00"},
		{cell("rise", 0, 0.125, 0), "rise 12.5%"},
	} {
		if tc.got != tc.want {
			t.Errorf("watchCell = %q, want %q", tc.got, tc.want)
		}
	}
}

// The alert names the anchor, the movement and when the anchor was set. "Down
// 10%" with nothing to measure it against is unfalsifiable from the alert
// alone, and an alert a reader cannot check is one they learn to skim.
func TestWatchAlertNamesTheAnchor(t *testing.T) {
	price, anchor := 34.57, 38.43
	w := store.WatchStatus{
		Watch: store.Watch{Op: "drop", Pct: 0.10},
		Name:  "Prismatic Vista", SetCode: "mh1", CollectorNumber: "244",
		PriceUSD: &price, Anchor: &anchor, AnchorAt: "2026-06-24T12:00:00Z",
	}
	got := watchAlert(plainEnv(), w, "")
	for _, want := range []string{"$34.57", "down", "10.0%", "$38.43", "high"} {
		if !strings.Contains(got, want) {
			t.Errorf("alert %q is missing %q", got, want)
		}
	}
	// The prose says down and up; drop and rise are the row's names for the
	// rule, not the report of what happened.
	if strings.Contains(got, "drop") {
		t.Errorf("alert %q used the op where it should read as prose", got)
	}

	up := 51.96
	low := 46.54
	w = store.WatchStatus{
		Watch: store.Watch{Op: "rise", Pct: 0.10},
		Name:  "Warren Soultrader", SetCode: "mh3", CollectorNumber: "332",
		PriceUSD: &up, Anchor: &low, AnchorAt: "2026-05-21T12:00:00Z",
	}
	got = watchAlert(plainEnv(), w, " foil")
	for _, want := range []string{"foil", "up", "11.6%", "$46.54", "low"} {
		if !strings.Contains(got, want) {
			t.Errorf("alert %q is missing %q", got, want)
		}
	}
}

// A percent watch whose record does not reach back far enough is not waiting
// on a price. Saying "waiting" for both would leave the watch that can never
// fire looking exactly like the one that might.
func TestCmdWatchListShowsWaitingOnHistory(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()
	if err := execWatch(ctx, st, []string{"add", "Sol", "Ring", "--drop", "10%"}, false); err != nil {
		t.Fatalf("watch add: %v", err)
	}
	// A series that begins inside its own window: a 30-day high cannot be read
	// off three days of a printing's record.
	seedSeries(t, st, map[string]float64{daysAgo(3): 99.00, daysAgo(1): 60.00})
	out := mustExec(t, ctx, st, []string{"watch", "list"})
	if !strings.Contains(out, "waiting on history") {
		t.Errorf("watch list = %q, want the waiting-on-history state", out)
	}
	if err := execWatch(ctx, st, nil, false); err != nil {
		t.Fatalf("the check fired on a series with no reach: %v", err)
	}
}

// A file can stand movements and dollar lines side by side, through the same
// one-pass resolve an absolute import already uses. The stdin spelling is
// untouched by any of it.
func TestCmdWatchImportPercentRows(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	withStdin(t, "Name,Direction,Threshold,Percent,Min Move,Since\n"+
		"Sol Ring,under,2,,,\n"+
		"Sol Ring,drop,,10%,1,60d\n")

	if err := execWatch(context.Background(), st, []string{"import", "-"}, false); err != nil {
		t.Fatalf("watch import -: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 2 {
		t.Fatalf("watches = %+v, %v, want the movement and the line side by side", watches, err)
	}
	var drop, under store.WatchStatus
	for _, w := range watches {
		switch w.Op {
		case "drop":
			drop = w
		case "under":
			under = w
		}
	}
	if drop.Pct != 0.10 || drop.MinMove != 1 || drop.WindowDays != 60 || drop.Threshold != 0 {
		t.Errorf("drop watch = %+v", drop)
	}
	if under.Threshold != 2 || under.Pct != 0 {
		t.Errorf("under watch = %+v", under)
	}
}
