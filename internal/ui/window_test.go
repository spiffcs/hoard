package ui

import (
	"strings"
	"testing"
)

func windowFixture(rows int) Table {
	t := Table{
		Header: true,
		Cols: []Col{
			{Title: "", Align: Left},
			{Title: "NAME", Align: Left},
			{Title: "VALUE", Align: Right},
		},
	}
	for i := range rows {
		t.Add(C("*"), C("R"+strings.Repeat("o", i)+"w"), C(Money(float64(i)*1.5)))
	}
	return t
}

func TestWindowLinesMatchTheWholeTable(t *testing.T) {
	tbl := windowFixture(200)
	all := tbl.Lines()

	for _, c := range []struct{ start, count int }{
		{0, 1}, {0, 30}, {5, 30}, {150, 30}, {199, 1}, {190, 30}, {0, 200}, {0, 500},
	} {
		got := tbl.WindowLines(c.start, c.count)
		end := min(c.start+c.count, 200)

		want := append([]string{all[0]}, all[1+c.start:1+end]...)
		if len(got) != len(want) {
			t.Fatalf("WindowLines(%d,%d) returned %d lines, want %d",
				c.start, c.count, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("WindowLines(%d,%d) line %d =\n%q\nwant\n%q",
					c.start, c.count, i, got[i], want[i])
			}
		}
	}
}

func TestWindowLinesKeepColumnWidthsStable(t *testing.T) {
	tbl := windowFixture(200)
	full := tbl.Lines()[1]
	narrow := tbl.WindowLines(0, 1)[1]
	if len(full) != len(narrow) {
		t.Errorf("row 0 is %d bytes in a full render and %d in a 1-row window — "+
			"column widths must not depend on the window:\n%q\n%q",
			len(full), len(narrow), full, narrow)
	}
}
