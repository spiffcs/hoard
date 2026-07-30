package report

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func testCollection() store.CollectionTotals {
	return store.CollectionTotals{DistinctCards: 2, TotalCopies: 10, Value: 100}
}

// Deliberately supplied out of order, and with one unpriced deck, so the sort
// and the zero-value bar are both exercised.
func testDecks() []store.DeckSummary {
	mk := func(name string, copies int, value float64) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: copies, Value: value}
		d.Name = name
		d.Kind = store.KindDeck
		return d
	}
	return []store.DeckSummary{
		mk("Beta Deck (Precon)", 60, 100),
		mk("Unpriced Deck (Precon)", 40, 0),
		mk("Alpha Deck (Precon)", 100, 300),
	}
}

func renderSummary(env ui.Env) string {
	return Summary(env, testCollection(), testDecks())
}

func TestSummaryTable(t *testing.T) {
	got := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	want := strings.Join([]string{
		"BINDER                     10  $100.00  ██",
		"DECKS · 3                 200  $400.00  ████████",
		"",
		"  Alpha Deck (Precon)     100  $300.00  ██████",
		"  Beta Deck (Precon)       60  $100.00  ██",
		"  Unpriced Deck (Precon)   40    $0.00",
		"",
		"TOTAL                     210  $500.00",
		"",
	}, "\n")
	if got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}
}

// Decks descend by value; the collection and decks sections must sum to TOTAL.
func TestSummaryTableSortsByValue(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	alpha := strings.Index(out, "Alpha")
	beta := strings.Index(out, "Beta")
	unpriced := strings.Index(out, "Unpriced")
	if !(alpha < beta && beta < unpriced) {
		t.Errorf("decks not ordered by value desc:\n%s", out)
	}
}

// A tie in value falls back to name order, so unpriced hoards don't shuffle.
func TestSummaryTableTieBreaksByName(t *testing.T) {
	mk := func(name string) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: 1}
		d.Name = name
		return d
	}
	decks := []store.DeckSummary{mk("Zeta"), mk("Alpha"), mk("Mid")}
	out := Summary(ui.Env{Width: 80, Clamp: true}, store.CollectionTotals{}, decks)
	a, m, z := strings.Index(out, "Alpha"), strings.Index(out, "Mid"), strings.Index(out, "Zeta")
	if !(a < m && m < z) {
		t.Errorf("equal-value decks not name-ordered:\n%s", out)
	}
}

// The non-terminal profile is the scriptable one: whole names, no bars, and
// crucially no escape sequences.
func TestSummaryTablePiped(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: false, Bars: false})
	if strings.Contains(out, "\x1b") {
		t.Error("piped summary contains ANSI escapes")
	}
	if strings.ContainsAny(out, "█▏▎▍▌▋▊▉") {
		t.Error("piped summary contains share bars")
	}
	if strings.Contains(out, "…") {
		t.Error("piped summary truncated a name")
	}
	if !strings.Contains(out, "Unpriced Deck (Precon)") {
		t.Errorf("piped summary lost a full deck name:\n%s", out)
	}
}

// At a narrow width the layout must still fit exactly — that is the whole point
// of truncating rather than letting rows wrap and break the bar column.
func TestSummaryTableNarrow(t *testing.T) {
	for _, width := range []int{60, 50, 40, 30} {
		env := ui.Env{Width: width, Clamp: true, Bars: width >= 50}
		for line := range strings.SplitSeq(renderSummary(env), "\n") {
			if w := len([]rune(line)); w > width {
				t.Errorf("width %d: line %q is %d wide", width, line, w)
			}
		}
	}
}

// An empty hoard must render zeros rather than dividing by a zero grand total.
func TestSummaryTableEmpty(t *testing.T) {
	out := Summary(ui.Env{Width: 80, Clamp: true, Bars: true},
		store.CollectionTotals{}, nil)
	// "DECKS · 0" is now the widest label, so it sets the name column.
	want := strings.Join([]string{
		"BINDER     0  $0.00",
		"DECKS · 0  0  $0.00",
		"",
		"TOTAL      0  $0.00",
		"",
	}, "\n")
	if out != want {
		t.Errorf("empty summary =\n%s\nwant\n%s", out, want)
	}
}

// The list is sorted by what a move did to the hoard, not by the change in
// sticker price. A dime on forty copies beats a dollar on one.
func TestMoverSectionsSortByTotalImpact(t *testing.T) {
	changes := []store.PriceChange{
		change("One Dollar Each", 1, 10.00, 11.00),    // +$1.00
		change("A Dime, Forty Times", 40, 1.00, 1.10), // +$4.00
		change("Crashed", 2, 20.00, 15.00),            // -$10.00
		change("Slipped", 1, 5.00, 4.50),              // -$0.50
	}

	secs := moverSections(changes, 10)
	if len(secs) != 2 || secs[0].Title != "RISERS" || secs[1].Title != "SINKERS" {
		t.Fatalf("sections = %+v, want RISERS then SINKERS", secs)
	}

	var risers []string
	for _, c := range secs[0].Rows {
		risers = append(risers, c.Name)
	}
	if strings.Join(risers, ",") != "A Dime, Forty Times,One Dollar Each" {
		t.Errorf("risers = %v, want the forty dimes first", risers)
	}

	var sinkers []string
	for _, c := range secs[1].Rows {
		sinkers = append(sinkers, c.Name)
	}
	if strings.Join(sinkers, ",") != "Crashed,Slipped" {
		t.Errorf("sinkers = %v, want the biggest loss first", sinkers)
	}
}

func TestMoverSectionsTruncate(t *testing.T) {
	var changes []store.PriceChange
	for i := range 25 {
		changes = append(changes, change("Riser", 1, 1.00, float64(2+i)))
		changes = append(changes, change("Sinker", 1, 100.00, float64(50-i)))
	}
	for _, sec := range moverSections(changes, 3) {
		if len(sec.Rows) != 3 {
			t.Errorf("%s has %d rows, want the limit of 3", sec.Title, len(sec.Rows))
		}
	}
	// A nonsensical limit falls back to the default rather than printing nothing.
	for _, sec := range moverSections(changes, 0) {
		if len(sec.Rows) != DefaultMoverRows {
			t.Errorf("%s has %d rows, want the default %d", sec.Title, len(sec.Rows), DefaultMoverRows)
		}
	}
}

// A card that moved but is held nowhere never reaches the sections; one that is
// held but did not move is not a change. Both are the store's job — what this
// checks is that a zero-delta row cannot land in either section, which would
// print a "riser" that rose by nothing.
func TestMoverSectionsExcludeUnmoved(t *testing.T) {
	secs := moverSections([]store.PriceChange{change("Flat", 3, 5.00, 5.00)}, 10)
	for _, sec := range secs {
		if len(sec.Rows) != 0 {
			t.Errorf("%s = %+v, want nothing for a price that did not move", sec.Title, sec.Rows)
		}
	}
}

func TestMoversTable(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: "foil", Copies: 3, Old: 10.00, New: 12.50},
	}
	got := moversTable(ui.Env{Width: 100, Clamp: true}, moverSections(rows, 10)).Render()

	for _, want := range []string{
		"RISERS",
		"Ulamog, the Infinite Gyre", "uma/7", "foil",
		"$10.00", "→", "$12.50",
		"+25.0%", // the per-copy move
		"×3",     // how many are held
		"+$7.50", // what that did to the hoard
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered row is missing %q:\n%s", want, got)
		}
	}
	// A section with nothing in it prints no heading.
	if strings.Contains(got, "SINKERS") {
		t.Errorf("empty section still printed its heading:\n%s", got)
	}
}

// Non-foil is the common case and says nothing worth a column of repetition.
func TestMoversTableMarksOnlyInterestingFinishes(t *testing.T) {
	got := moversTable(ui.Env{Width: 100, Clamp: true},
		moverSections([]store.PriceChange{change("Sol Ring", 1, 2.00, 3.00)}, 10)).Render()
	if strings.Contains(got, "normal") {
		t.Errorf("non-foil rows should not spell out the finish:\n%s", got)
	}
}

// Both halves must line up: laid out separately, a five-figure sinker widens its
// price columns and a narrow terminal drops different columns from each half,
// printing two differently shaped tables for the same report.
func TestMoversTableSharesOneLayoutAcrossSections(t *testing.T) {
	changes := []store.PriceChange{
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "1", Finish: "normal",
			Copies: 40, Old: 1.00, New: 1.10},
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: "normal",
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	out := moversTable(ui.Env{Width: 70, Clamp: true}, moverSections(changes, 10)).Render()

	var riser, sinker string
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(line, "Sol Ring"):
			riser = line
		case strings.Contains(line, "Black Lotus"):
			sinker = line
		}
	}
	if riser == "" || sinker == "" {
		t.Fatalf("expected a row in each section:\n%s", out)
	}
	// Both rows carry the same columns, so the impact figures line up: the same
	// number of fields, ending in the money that sorted them.
	if got, want := len(strings.Fields(riser)), len(strings.Fields(sinker)); got != want {
		t.Errorf("sections rendered different column sets (%d vs %d fields):\n%s", got, want, out)
	}
}

// The table has to survive a narrow terminal without wrapping, since it prints
// straight after a refresh in whatever window the user has open. The five-figure
// row is the demanding case: it is what widens the money columns and forces
// columns to be given up.
func TestMoversTableFitsNarrowTerminal(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: "foil", Copies: 3, Old: 10.00, New: 12.50},
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: "normal",
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	for _, width := range []int{44, 60, 80, 120} {
		out := moversTable(ui.Env{Width: width, Clamp: true}, moverSections(rows, 10)).Render()
		for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Errorf("at width %d a line is %d cells wide: %q", width, len([]rune(line)), line)
			}
		}
		// Whatever else is given up, the report is the impact figures.
		for _, want := range []string{"+$7.50", "-$1,500.00"} {
			if !strings.Contains(out, want) {
				t.Errorf("at width %d the impact %s was dropped:\n%s", width, want, out)
			}
		}
	}
}

// An arrow with nothing on its left is nonsense, so the old price must not be
// given up before the arrow that points away from it.
func TestMoversTableDropsTheArrowBeforeTheOldPrice(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: "normal",
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	for width := 40; width <= 120; width++ {
		out := moversTable(ui.Env{Width: width, Clamp: true}, moverSections(rows, 10)).Render()
		if strings.Contains(out, "→") && !strings.Contains(out, "$20,000.00") {
			t.Fatalf("at width %d the arrow outlived the price it points from:\n%s", width, out)
		}
	}
}

func TestSignedMoney(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{7.5, "+$7.50"},
		{-7.5, "-$7.50"},
		{0, "$0.00"},
		{1234.5, "+$1,234.50"},
	} {
		if got := ui.SignedMoney(tc.in); got != tc.want {
			t.Errorf("ui.SignedMoney(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ui.Percent renders anything at or below zero as empty, which is exactly the
// half of this list that matters, so movers has its own.
func TestSignedPercent(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0.25, "+25.0%"},
		{-0.25, "-25.0%"},
		{0.005, "+0.5%"},
		{0, ""}, // no old price to be a percentage of
	} {
		if got := ui.SignedPercent(tc.in); got != tc.want {
			t.Errorf("ui.SignedPercent(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func change(name string, copies int, old, now float64) store.PriceChange {
	return store.PriceChange{
		Name: name, SetCode: "uma", CollectorNumber: "7", Finish: "normal",
		Copies: copies, Old: old, New: now,
	}
}
