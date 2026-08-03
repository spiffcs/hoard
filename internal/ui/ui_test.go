package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMoney(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.5, "$0.50"},
		{1753.2, "$1,753.20"}, // the trailing zero go-humanize would drop
		{1753.27, "$1,753.27"},
		{999.995, "$1,000.00"},
		{1000, "$1,000.00"},
		{3889.28, "$3,889.28"},
		{1234567.5, "$1,234,567.50"},
		{-12, "-$12.00"},
	}
	for _, c := range cases {
		if got := Money(c.in); got != c.want {
			t.Errorf("Money(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMoneyPtr(t *testing.T) {
	if got := MoneyPtr(nil); got != unknown {
		t.Errorf("MoneyPtr(nil) = %q, want %q", got, unknown)
	}
	v := 4.5
	if got := MoneyPtr(&v); got != "$4.50" {
		t.Errorf("MoneyPtr(&4.5) = %q, want $4.50", got)
	}
}

func TestCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"}, {94, "94"}, {999, "999"}, {1000, "1,000"},
		{1878, "1,878"}, {1972, "1,972"}, {1234567, "1,234,567"}, {-1500, "-1,500"},
	}
	for _, c := range cases {
		if got := Count(c.in); got != c.want {
			t.Errorf("Count(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBar(t *testing.T) {
	cases := []struct {
		name  string
		frac  float64
		cells int
		want  string
	}{
		{"zero renders nothing", 0, 10, ""},
		{"negative renders nothing", -0.5, 10, ""},
		{"no cells", 0.5, 0, ""},
		{"tiny nonzero clamps to narrowest glyph", 1e-9, 10, "▏"},
		// The four shares verified against the live database.
		{"collection 48.9%", 0.4890, 10, "████▉"},
		{"decks 51.1%", 0.5110, 10, "█████"},
		{"top deck 5.1%", 0.0509, 10, "▌"},
		{"smallest deck 0.4%", 0.0039, 10, "▏"},
		{"full", 1, 10, "██████████"},
		{"over-full clamps", 1.5, 10, "██████████"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Bar(c.frac, c.cells); got != c.want {
				t.Errorf("Bar(%v, %d) = %q, want %q", c.frac, c.cells, got, c.want)
			}
		})
	}
}

// Bars must never claim more of the column than the share warrants.
func TestBarNeverExceedsCells(t *testing.T) {
	for _, frac := range []float64{0.01, 0.33, 0.5, 0.99, 1.0} {
		for _, cells := range []int{6, 10, 14} {
			if w := ansi.StringWidth(Bar(frac, cells)); w > cells {
				t.Errorf("Bar(%v, %d) width %d exceeds %d", frac, cells, w, cells)
			}
		}
	}
}

func TestTruncate(t *testing.T) {
	const long = "Graveyard Overdrive Collector's Edition (Modern Horizons 3 Commander Precon)"
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"}, // exact fit is not truncated
		{"abcdef", 5, "abcd…"},
		{"abcdef", 1, "…"},
		{"Æther Vial", 20, "Æther Vial"},
		{"Æther Vial", 6, "Æther…"},
		{long, 48, "Graveyard Overdrive Collector's Edition (Modern…"},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.w)
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		if w := ansi.StringWidth(got); c.w > 0 && w > c.w {
			t.Errorf("Truncate(%q, %d) width %d exceeds %d", c.in, c.w, w, c.w)
		}
	}
}

// summaryCols mirrors the column set cmdSummary declares, so the ladder test
// exercises the real layout rather than a synthetic one.
func summaryCols() []Col {
	return []Col{
		{Title: "NAME", Align: Left, Flex: true, Min: 12},
		{Title: "CARDS", Align: Right, Priority: 2},
		{Title: "VALUE", Align: Right},
		{Title: "", Align: Left, Min: 6, Max: 10, Priority: 3},
	}
}

// natural widths for the real data: longest name 76, "1,878" 5, "$3,889.28" 9,
// full bar 10.
var summaryNatural = []int{76, 5, 9, 10}

func TestFitColumns(t *testing.T) {
	cases := []struct {
		width    int
		wantName int
		wantBar  int
		wantKeep []bool
	}{
		{200, 76, 10, []bool{true, true, true, true}}, // nothing truncated
		{106, 76, 10, []bool{true, true, true, true}}, // exact natural fit
		{80, 50, 10, []bool{true, true, true, true}},  // the common case
		{54, 24, 10, []bool{true, true, true, true}},  // name still above Min
		{50, 20, 10, []bool{true, true, true, true}},  // name shrinking
		{42, 12, 10, []bool{true, true, true, true}},  // name at Min, bar intact
		{36, 18, 0, []bool{true, true, true, false}},  // bar dropped, name reclaims
		{30, 12, 0, []bool{true, true, true, false}},  // name back to Min
		{25, 14, 0, []bool{true, false, true, false}}, // cards dropped, name reclaims
	}
	for _, c := range cases {
		env := Env{Width: c.width, Clamp: true, Bars: true}
		widths, keep := fitColumns(summaryCols(), summaryNatural, DefaultGutter, env)

		for i, want := range c.wantKeep {
			if keep[i] != want {
				t.Errorf("width %d: keep[%d] = %v, want %v", c.width, i, keep[i], want)
			}
		}
		if widths[0] != c.wantName {
			t.Errorf("width %d: name = %d, want %d", c.width, widths[0], c.wantName)
		}
		if keep[3] && widths[3] != c.wantBar {
			t.Errorf("width %d: bar = %d, want %d", c.width, widths[3], c.wantBar)
		}

		// The whole point of the ladder: the line must actually fit.
		total, n := 0, 0
		for i := range summaryCols() {
			if keep[i] {
				total += widths[i]
				n++
			}
		}
		total += (n - 1) * DefaultGutter
		if total > c.width {
			t.Errorf("width %d: laid out %d columns wide, overflows", c.width, total)
		}
	}
}

// A non-terminal must never truncate: full names, greppable.
func TestFitColumnsNoClamp(t *testing.T) {
	env := Env{Width: 80, Clamp: false}
	widths, keep := fitColumns(summaryCols(), summaryNatural, DefaultGutter, env)
	for i, w := range widths {
		if w != summaryNatural[i] || !keep[i] {
			t.Errorf("col %d: got width %d keep %v, want natural %d kept", i, w, keep[i], summaryNatural[i])
		}
	}
}

func testTable(env Env) Table {
	t := Table{
		Env:    env,
		Header: true,
		Cols: []Col{
			{Title: "NAME", Align: Left, Flex: true, Min: 8},
			{Title: "QTY", Align: Right},
			{Title: "VALUE", Align: Right},
		},
	}
	t.Add(C("Sol Ring"), C("4"), C("$12.00"))
	t.Add(C("Mana Crypt"), C("1"), C("$1,234.50"))
	t.AddSpacer()
	t.AddStyled(env.Bold(), C("TOTAL"), C("5"), C("$1,246.50"))
	return t
}

func TestRenderPlain(t *testing.T) {
	got := testTable(Env{Width: 40, Clamp: true}).Render()
	want := strings.Join([]string{
		"NAME        QTY      VALUE",
		"Sol Ring      4     $12.00",
		"Mana Crypt    1  $1,234.50",
		"",
		"TOTAL         5  $1,246.50",
		"",
	}, "\n")
	if got != want {
		t.Errorf("Render() =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "\x1b") {
		t.Error("plain render contains ANSI escapes")
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line has trailing padding: %q", line)
		}
	}
}

// The regression test for the reason tabwriter was dropped: styling a cell must
// not shift any column, because escapes are not display width.
func TestRenderStyledAlignment(t *testing.T) {
	env := Env{Width: 40, Clamp: true}
	plainLines := strings.Split(testTable(env).Render(), "\n")

	env.Color = true
	styledLines := strings.Split(testTable(env).Render(), "\n")

	if len(plainLines) != len(styledLines) {
		t.Fatalf("line count differs: %d vs %d", len(plainLines), len(styledLines))
	}
	sawEscape := false
	for i := range plainLines {
		if strings.Contains(styledLines[i], "\x1b") {
			sawEscape = true
		}
		pw, sw := ansi.StringWidth(plainLines[i]), ansi.StringWidth(styledLines[i])
		if pw != sw {
			t.Errorf("line %d: display width %d styled vs %d plain\n plain: %q\nstyled: %q",
				i, sw, pw, plainLines[i], styledLines[i])
		}
	}
	if !sawEscape {
		t.Skip("terminal profile suppressed all styling; alignment check was trivial")
	}
}

func TestRenderTruncatesToWidth(t *testing.T) {
	env := Env{Width: 24, Clamp: true}
	tbl := Table{Env: env, Cols: []Col{
		{Title: "NAME", Align: Left, Flex: true, Min: 8},
		{Title: "VALUE", Align: Right},
	}}
	tbl.Add(C("Duel Decks Anthology: Divine vs. Demonic (Divine)"), C("$22.37"))

	for line := range strings.SplitSeq(tbl.Render(), "\n") {
		if w := ansi.StringWidth(line); w > env.Width {
			t.Errorf("line %q is %d wide, exceeds %d", line, w, env.Width)
		}
	}
	if !strings.Contains(tbl.Render(), "…") {
		t.Error("expected an ellipsis marking the truncation")
	}
}

// Render must stay byte-identical now that it is built from Lines, or every
// piped table in the CLI shifts.
func TestLinesJoinToRender(t *testing.T) {
	env := Env{Width: 60, Clamp: true}
	tb := testTable(env)
	lines := tb.Lines()
	if got, want := strings.Join(lines, "\n")+"\n", tb.Render(); got != want {
		t.Errorf("Lines joined = %q\nRender      = %q", got, want)
	}
}

// The contract an interactive list depends on: with a header and no spacers,
// row i is line i+1. If that slips, a cursor highlights the wrong card.
func TestLinesRowIndexing(t *testing.T) {
	tb := Table{
		Env:    Env{Width: 40, Clamp: true},
		Header: true,
		Cols:   []Col{{Title: "NAME", Align: Left}, {Title: "QTY", Align: Right}},
	}
	tb.Add(C("Sol Ring"), C("40"))
	tb.Add(C("Bitterblossom"), C("4"))

	lines := tb.Lines()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want header + 2 rows: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "NAME") {
		t.Errorf("line 0 = %q, want the header", lines[0])
	}
	if !strings.Contains(lines[1], "Sol Ring") {
		t.Errorf("line 1 = %q, want row 0", lines[1])
	}
	if !strings.Contains(lines[2], "Bitterblossom") {
		t.Errorf("line 2 = %q, want row 1", lines[2])
	}
	// No line carries its own newline; the caller joins them.
	for i, l := range lines {
		if strings.Contains(l, "\n") {
			t.Errorf("line %d contains a newline: %q", i, l)
		}
	}
}

func TestLinesSpacerIsAnEmptyLine(t *testing.T) {
	tb := Table{Env: Env{Width: 40, Clamp: true}, Cols: []Col{{Title: "N", Align: Left}}}
	tb.Add(C("a"))
	tb.AddSpacer()
	tb.Add(C("b"))

	lines := tb.Lines()
	if len(lines) != 3 || lines[1] != "" {
		t.Errorf("lines = %q, want a spacer as an empty middle line", lines)
	}
}

func TestSpark(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		cells  int
		want   string
	}{
		{"rising", []float64{1, 2, 3, 4, 5, 6, 7, 8}, 8, "▁▂▃▄▅▆▇█"},
		{"falling", []float64{8, 7, 6, 5, 4, 3, 2, 1}, 8, "█▇▆▅▄▃▂▁"},
		{"two points", []float64{1, 2}, 8, "▁█"},
		{"single point", []float64{42}, 8, "▄"},
		// A card whose price never moved must not look like one that bottomed out.
		{"flat", []float64{5, 5, 5, 5}, 8, "▄▄▄▄"},
		{"empty", nil, 8, ""},
		{"no cells", []float64{1, 2, 3}, 0, ""},
		// Negatives are not prices, but the scale must not assume a zero floor.
		{"negative range", []float64{-4, 0, 4}, 3, "▁▄█"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Spark(tt.values, tt.cells); got != tt.want {
				t.Errorf("Spark(%v, %d) = %q, want %q", tt.values, tt.cells, got, tt.want)
			}
		})
	}
}

func TestSparkNeverExceedsCells(t *testing.T) {
	long := make([]float64, 500)
	for i := range long {
		long[i] = float64(i % 17)
	}
	for _, cells := range []int{1, 5, 20, 80} {
		got := Spark(long, cells)
		if n := len([]rune(got)); n != cells {
			t.Errorf("Spark(500 values, %d) rendered %d cells", cells, n)
		}
	}
}

// A NaN would otherwise become the whole range and flatten every real point.
func TestSparkIgnoresNonFiniteValues(t *testing.T) {
	got := Spark([]float64{1, math.NaN(), 8, math.Inf(1)}, 8)
	if got != "▁█" {
		t.Errorf("Spark with NaN/Inf = %q, want the two finite points", got)
	}
}

// Heat runs green to red: the endpoints match the ramp's own anchors
// (computed through it, never hardcoded — the termenv round-trip lesson),
// the midpoint differs from both, and a colorless env stays plain.
func TestHeatRamp(t *testing.T) {
	e := Env{Color: true}
	lo, mid, hi := e.Heat(0)("x"), e.Heat(0.5)("x"), e.Heat(1)("x")
	if lo == mid || mid == hi || lo == hi {
		t.Errorf("ramp does not move: %q %q %q", lo, mid, hi)
	}
	if e.Heat(-1)("x") != lo || e.Heat(2)("x") != hi {
		t.Errorf("ramp must clamp at its ends")
	}
	if got := (Env{}).Heat(1)("x"); got != "x" {
		t.Errorf("colorless heat = %q, want plain", got)
	}
	// Agreement wears the same green Grade's top end does.
	if lo != e.Grade(1)("x") {
		t.Errorf("heat's green end must match grade's: %q vs %q", lo, e.Grade(1)("x"))
	}
}
