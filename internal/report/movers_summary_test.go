package report

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

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

// The defect this pins: a longer window can report a smaller count, because a
// printing needs a price recorded at or before the cutoff to have a baseline at
// all and one first priced later is left out rather than reported as flat. On
// the owner's database a 30-day window compares 1,963 printings and reports
// 1,661 movers, while a 103-day window compares 499 and reports 474. Said as
// "474 printings moved", the wider window reads as the hoard going quiet.
//
// The sentence has to name the population it counted over. Against the previous
// wording -- "%s printings moved %s. Net change: %s" -- this fails.
func TestMoversSummaryNamesThePopulationItCountedOver(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since 11 Jul 2026")
	summary := lastLine(t, got)

	want := "2 printings moved since 11 Jul 2026, among those priced by then. Net change: +$3.50"
	if summary != want {
		t.Errorf("summary = %q,\n   want %q", summary, want)
	}
	if strings.HasSuffix(summary, "moved since 11 Jul 2026. Net change: +$3.50") {
		t.Errorf("summary still claims movement with no population named: %q", summary)
	}
}

// The empty report has the same defect and needs the same repair: with nothing
// comparable in the window, "No price changes since 29 Apr 2026" asserts the
// hoard held still when what happened is that nothing had a baseline to be
// measured from.
func TestMoversEmptySummaryNamesThePopulationItCountedOver(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, nil, 10, "since 29 Apr 2026")

	want := "No price changes since 29 Apr 2026, among printings priced by then.\n"
	if got != want {
		t.Errorf("empty report = %q,\n          want %q", got, want)
	}
}

// The clean-case control. This is a legibility fix, so nothing that was
// counted may have moved: the table is compared to the captured bytes of the
// previous revision, and the two figures in the sentence -- the count and the
// net -- have to be the same two figures that revision printed.
func TestMoversRewordingChangedNoFigure(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since 11 Jul 2026")

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
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since the last refresh")

	want := "2 printings moved since the last refresh, among those priced by then. Net change: +$3.50"
	if summary := lastLine(t, got); summary != want {
		t.Errorf("summary = %q,\n   want %q", summary, want)
	}
}

// lastLine is the summary sentence: the report ends with it and a newline.
func lastLine(t *testing.T, report string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(report, "\n"), "\n")
	return lines[len(lines)-1]
}
