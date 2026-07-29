package main

import (
	"strings"
	"testing"

	"github.com/cphillips918/hoard/internal/store"
	"github.com/cphillips918/hoard/internal/ui"
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
	return summaryTable(env, testCollection(), testDecks()).Render()
}

func TestSummaryTable(t *testing.T) {
	got := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	want := strings.Join([]string{
		"COLLECTION                 10  $100.00  ██",
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
	out := summaryTable(ui.Env{Width: 80, Clamp: true}, store.CollectionTotals{}, decks).Render()
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
	out := summaryTable(ui.Env{Width: 80, Clamp: true, Bars: true},
		store.CollectionTotals{}, nil).Render()
	want := strings.Join([]string{
		"COLLECTION  0  $0.00",
		"DECKS · 0   0  $0.00",
		"",
		"TOTAL       0  $0.00",
		"",
	}, "\n")
	if out != want {
		t.Errorf("empty summary =\n%s\nwant\n%s", out, want)
	}
}
