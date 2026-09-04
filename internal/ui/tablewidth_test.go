package ui

import (
	"strings"
	"testing"
)

func TestTableWidthMatchesWhatItDraws(t *testing.T) {
	tbl := Table{
		Env:    Env{Width: 200, Clamp: true},
		Header: true,
		Cols: []Col{
			{Title: "NAME", Align: Left, Flex: true, Min: 4},
			{Title: "QTY", Align: Right},
			{Title: "VALUE", Align: Right},
		},
	}
	tbl.Add(C("Lightning Bolt"), C("×4"), C("$20.00"))
	tbl.Add(C("Wasteland"), C("×3"), C("$149.52"))

	widest := 0
	for _, line := range tbl.Lines() {
		widest = max(widest, Width(strings.TrimRight(line, " ")))
	}
	if got := tbl.Width(); got != widest {
		t.Errorf("Width() = %d, want the %d columns it actually draws", got, widest)
	}
}

func TestTableWidthDropsWhatTheTableDrops(t *testing.T) {
	tbl := Table{
		Env:    Env{Width: 8, Clamp: true},
		Header: true,
		Cols: []Col{
			{Title: "NAME", Align: Left, Flex: true, Min: 4},
			{Title: "GONE", Align: Left, Priority: 9},
		},
	}
	tbl.Add(C("Lightning Bolt"), C("shed me"))

	if got := tbl.Width(); got > 8 {
		t.Errorf("Width() = %d, want it to respect the 8-column budget", got)
	}
	if strings.Contains(strings.Join(tbl.Lines(), "\n"), "shed me") {
		t.Fatalf("the table kept a column it had no room for:\n%s",
			strings.Join(tbl.Lines(), "\n"))
	}
	if got := tbl.Width(); got != Width(strings.TrimRight(tbl.Lines()[1], " ")) {
		t.Errorf("Width() = %d disagrees with the drawn line %q", got, tbl.Lines()[1])
	}
}
