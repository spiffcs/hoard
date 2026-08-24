package report

import (
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

var testCutoff = time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)

func summaryRows() []store.PriceChange {
	return []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: finish.Foil, Copies: 3, Old: 10.00, New: 12.50},
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 40, Old: 1.00, New: 0.90},
	}
}

const goldenMoversTable = "" +
	"NAME                         SET/NUM  FINISH     WAS        NOW  CHANGE  QTY  IMPACT\n" +
	"RISERS\n" +
	"  Ulamog, the Infinite Gyre  uma/7    foil    $10.00  →  $12.50  +25.0%   ×3  +$7.50\n" +
	"\n" +
	"SINKERS\n" +
	"  Sol Ring                   c21/1    -        $1.00  →   $0.90  -10.0%  ×40  -$4.00\n" +
	"\n"

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

func TestMoversEmptySummaryNamesThePopulationItCountedOver(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, nil, 10, "since 29 Apr 2026", testCutoff)

	want := "No price changes since 29 Apr 2026, among printings priced more than once.\n"
	if got != want {
		t.Errorf("empty report = %q,\n          want %q", got, want)
	}
}

func TestMoversRewordingChangedNoFigure(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10, "since 11 Jul 2026", testCutoff)

	table, _, ok := strings.Cut(got, "2 printings moved")
	if !ok {
		t.Fatalf("summary sentence no longer starts with the count:\n%s", got)
	}
	if table != goldenMoversTable {
		t.Errorf("table changed.\n got %q\nwant %q", table, goldenMoversTable)
	}

	summary := lastLine(t, got)
	for _, figure := range []string{"2 printings", "Net change: +$3.50"} {
		if !strings.Contains(summary, figure) {
			t.Errorf("summary %q dropped %q", summary, figure)
		}
	}
}

func TestMoversSummaryReadsForTheRefreshWindow(t *testing.T) {
	got := Movers(ui.Env{Width: 100, Clamp: true}, summaryRows(), 10,
		"since the last refresh", time.Time{})

	want := "2 printings moved since the last refresh, among those priced more than once. Net change: +$3.50"
	if summary := lastLine(t, got); summary != want {
		t.Errorf("summary = %q,\n   want %q", summary, want)
	}
}

func TestMoversDatesARowThatStartsInsideTheWindow(t *testing.T) {
	rows := summaryRows()
	rows[0].OldAsOf = "2026-07-30T12:00:00Z"
	rows[1].OldAsOf = "2026-07-02T12:00:00Z"

	got := Movers(ui.Env{Width: 100, Clamp: true}, rows, 10, "since 11 Jul 2026", testCutoff)

	if !strings.Contains(got, "FROM") {
		t.Errorf("no FROM column for a row measuring less than the window:\n%s", got)
	}
	want := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan")
	if !strings.Contains(got, want) {
		t.Errorf("row does not name its own start %q:\n%s", want, got)
	}

	if unwanted := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan"); strings.Contains(got, unwanted) {
		t.Errorf("dated a row the window already covers (%q):\n%s", unwanted, got)
	}
}

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

func lastLine(t *testing.T, report string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(report, "\n"), "\n")
	return lines[len(lines)-1]
}
