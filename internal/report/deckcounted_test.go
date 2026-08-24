package report

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func summaryWithALoanedDeck() string {
	mk := func(name string, copies int, value float64, counted bool) store.DeckSummary {
		d := store.DeckSummary{DistinctCards: copies, TotalCopies: copies,
			Value: value, Counted: counted}
		d.Name = name
		d.Kind = store.KindDeck
		return d
	}
	decks := []store.DeckSummary{
		mk("Kept Deck", 60, 100, true),
		mk("Loaner Deck", 40, 300, false),
	}
	return Summary(ui.Env{Width: 80, Clamp: true},
		store.CollectionTotals{DistinctCards: 2, TotalCopies: 10, Value: 100}, decks)
}

func lineWith(t *testing.T, out, want string) string {
	t.Helper()
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, want) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, out)
	return ""
}

func TestSummaryLeavesAnUncountedDeckOutOfTheDeckTotal(t *testing.T) {
	out := summaryWithALoanedDeck()

	decks := lineWith(t, out, "DECKS")
	if !strings.Contains(decks, "$100.00") {
		t.Errorf("DECKS row = %q, want $100.00 — the loaned deck's $300 must not count", decks)
	}
	if !strings.Contains(decks, "60") {
		t.Errorf("DECKS row = %q, want 60 copies — the loaned deck's 40 must not count", decks)
	}
}

func TestSummaryLeavesAnUncountedDeckOutOfTheGrandTotal(t *testing.T) {
	out := summaryWithALoanedDeck()

	total := lineWith(t, out, "TOTAL")
	if !strings.Contains(total, "$200.00") {
		t.Errorf("TOTAL row = %q, want $200.00 (binder $100 + kept deck $100)", total)
	}
	if !strings.Contains(total, "70") {
		t.Errorf("TOTAL row = %q, want 70 copies (binder 10 + kept deck 60)", total)
	}
}

func TestSummaryStillListsAnUncountedDeckAndMarksIt(t *testing.T) {
	out := summaryWithALoanedDeck()

	loaner := lineWith(t, out, "Loaner Deck")
	if !strings.Contains(loaner, "$300.00") {
		t.Errorf("loaned deck row = %q, want its own value still shown", loaner)
	}
	if !strings.Contains(loaner, "*") {
		t.Errorf("loaned deck row = %q, want a marker saying it does not count", loaner)
	}
	if kept := lineWith(t, out, "Kept Deck"); strings.Contains(kept, "*") {
		t.Errorf("a counted deck picked up the marker: %q", kept)
	}
	if !strings.Contains(out, "not counted toward your collection") {
		t.Errorf("summary carries no footnote explaining the marker:\n%s", out)
	}
}
