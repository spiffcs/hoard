package report

import (
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// testCutoff is the instant "since 11 Jul 2026" names, for the tests that care
// where a row's own baseline sits relative to it.
var testCutoff = time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)

// summaryRows is one riser and one sinker, sized so the rendered table is
// stable enough to compare byte for byte.
func summaryRows() []store.PriceChange {
	return []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: "foil", Copies: 3, Old: 10.00, New: 12.50},
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "1",
			Finish: "nonfoil", Copies: 40, Old: 1.00, New: 0.90},
	}
}

// goldenMoversTable is what the table rendered before the summary sentence was
// reworded, captured from the previous revision. It is here to hold the
// wording change to the wording: every figure, column and row in the report
// above the sentence has to survive untouched.
const goldenMoversTable = "" +
	"NAME                         SET/NUM  FINISH     WAS        NOW  CHANGE  QTY  IMPACT\n" +
	"RISERS\n" +
	"  Ulamog, the Infinite Gyre  uma/7    foil    $10.00  →  $12.50  +25.0%   ×3  +$7.50\n" +
	"\n" +
	"SINKERS\n" +
	"  Sol Ring                   c21/1    -        $1.00  →   $0.90  -10.0%  ×40  -$4.00\n" +
	"\n"

// The sentence has to name the population it counted over, because a count
// alone cannot say whether a short list means a quiet hoard or a thin record.
//
// The population it names changed with the window rule. It used to be "those
// priced by then", which was true while a printing without a price at the
// cutoff was dropped from the answer; a record that starts inside the window is
// now measured across the part it has, so the only printing left out is one
// priced exactly once, having nothing to compare against.
func TestMoversSummaryNamesThePopulationItCountedOver(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since 11 Jul 2026", testCutoff)
	summary := lastLine(t, got)

	want := "2 printings moved since 11 Jul 2026, among those priced more than once. Net change: +$3.50"
	if summary != want {
		t.Errorf("summary = %q,\n   want %q", summary, want)
	}
	if strings.Contains(summary, "priced by then") {
		t.Errorf("summary still names the population the old rule excluded on: %q", summary)
	}
}

// The empty report names the same population, for the same reason: with nothing
// to show, "No price changes since 29 Apr 2026" asserts the hoard held still,
// when what may have happened is that nothing had a second price to be measured
// against.
func TestMoversEmptySummaryNamesThePopulationItCountedOver(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, nil, 10, "since 29 Apr 2026", testCutoff)

	want := "No price changes since 29 Apr 2026, among printings priced more than once.\n"
	if got != want {
		t.Errorf("empty report = %q,\n          want %q", got, want)
	}
}

// The clean-case control, and the one that matters most now that the window
// rule decides what gets counted: on a window the history covers, nothing about
// the table may move. Every row here has a baseline at or before the cutoff, so
// the FROM column has nothing to say on any row and must not appear at all --
// the table is compared to the captured bytes of the revision before the rule
// changed, column for column. The two figures in the sentence, the count and
// the net, have to be the same two figures that revision printed.
func TestMoversRewordingChangedNoFigure(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since 11 Jul 2026", testCutoff)

	table, _, ok := strings.Cut(got, "2 printings moved")
	if !ok {
		t.Fatalf("summary sentence no longer starts with the count:\n%s", got)
	}
	if table != goldenMoversTable {
		t.Errorf("table changed.\n got %q\nwant %q", table, goldenMoversTable)
	}
	// Both figures the previous sentence carried, still carried.
	summary := lastLine(t, got)
	for _, figure := range []string{"2 printings", "Net change: +$3.50"} {
		if !strings.Contains(summary, figure) {
			t.Errorf("summary %q dropped %q", summary, figure)
		}
	}
}

// The wording has to hold for the other caller too: update-prices names its
// window as a refresh rather than a date, and the sentence is built once for
// both.
func TestMoversSummaryReadsForTheRefreshWindow(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10,
		"since the last refresh", time.Time{})

	want := "2 printings moved since the last refresh, among those priced more than once. Net change: +$3.50"
	if summary := lastLine(t, got); summary != want {
		t.Errorf("summary = %q,\n   want %q", summary, want)
	}
}

// A row whose record begins inside the window is measured from where its own
// record starts, and the table says so on that row rather than letting the
// header's date speak for it. This is the reported case: at thirty days, a
// printing first priced four days ago reports its four days.
func TestMoversDatesARowThatStartsInsideTheWindow(t *testing.T) {
	rows := summaryRows()
	rows[0].OldAsOf = "2026-07-30T12:00:00Z" // begins well after the 11 Jul cutoff
	rows[1].OldAsOf = "2026-07-02T12:00:00Z" // reaches back past it

	got := Movers(ui.Env{Width: 100, Clamp: true}, rows, 10, "since 11 Jul 2026", testCutoff)

	if !strings.Contains(got, "FROM") {
		t.Errorf("no FROM column for a row measuring less than the window:\n%s", got)
	}
	want := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan")
	if !strings.Contains(got, want) {
		t.Errorf("row does not name its own start %q:\n%s", want, got)
	}
	// The row the window covers says nothing: its figures are the window's, and
	// a date repeating the header is a column of noise.
	if unwanted := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan"); strings.Contains(got, unwanted) {
		t.Errorf("dated a row the window already covers (%q):\n%s", unwanted, got)
	}
}

// The equality control stated directly: baselines the window covers produce the
// table the rule change was required not to touch.
func TestMoversWithCoveredBaselinesRendersTheOldTable(t *testing.T) {
	rows := summaryRows()
	rows[0].OldAsOf = "2026-07-02T12:00:00Z"
	rows[1].OldAsOf = "2026-06-30T12:00:00Z"

	got := Movers(ui.Env{Width: 100, Clamp: true}, rows, 10, "since 11 Jul 2026", testCutoff)
	table, _, ok := strings.Cut(got, "2 printings moved")
	if !ok {
		t.Fatalf("summary sentence no longer starts with the count:\n%s", got)
	}
	if table != goldenMoversTable {
		t.Errorf("table changed on a window the history covers.\n got %q\nwant %q",
			table, goldenMoversTable)
	}
}

// lastLine is the summary sentence: the report ends with it and a newline.
func lastLine(t *testing.T, report string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(report, "\n"), "\n")
	return lines[len(lines)-1]
}
