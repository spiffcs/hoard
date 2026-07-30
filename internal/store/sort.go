package store

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// byValue returns a copy ordered most-valuable-first, with a name tiebreak.
//
// Every listing in hoard ranks this way, because what is wanted from all of them
// is "where is the money" — the store's own ORDER BY is only a stable base to
// sort from. The name tiebreak is what keeps a hoard whose prices were never
// fetched, where every row is $0.00, listing predictably instead of arbitrarily.
func byValue[T any](items []T, value func(T) float64, name func(T) string) []T {
	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b T) int {
		if c := cmp.Compare(value(b), value(a)); c != 0 {
			return c
		}
		return strings.Compare(name(a), name(b))
	})
	return sorted
}

// CollectionByValue orders loose-collection holdings most-valuable-first.
func CollectionByValue(rows []CollectionRow) []CollectionRow {
	return byValue(rows,
		func(r CollectionRow) float64 { return r.Value },
		func(r CollectionRow) string { return r.Name })
}

// DecksByValue orders decks most-valuable-first.
func DecksByValue(decks []DeckSummary) []DeckSummary {
	return byValue(decks,
		func(d DeckSummary) float64 { return d.Value },
		func(d DeckSummary) string { return d.Name })
}

// EntriesByValue orders a deck's cards most-valuable-first.
//
// The store returns them grouped by board, which this flattens: the board is
// still on every row, and "what is expensive in this deck" is the question a
// deck listing is usually asked.
func EntriesByValue(entries []EntryView) []EntryView {
	return byValue(entries,
		func(e EntryView) float64 { return e.Value() },
		func(e EntryView) string { return e.Card.Name })
}

// MoversByImpact orders price changes by what the move was worth across every
// copy held, largest first, regardless of direction.
//
// The CLI splits movers into a risers table and a sinkers table because it
// prints both in full. A single scrolling pane cannot do that without showing
// one and burying the other, so it interleaves them by magnitude: the top of the
// list is what moved the hoard most, whichever way it went.
func MoversByImpact(changes []PriceChange) []PriceChange {
	return byValue(changes,
		func(c PriceChange) float64 { return math.Abs(c.TotalDelta()) },
		func(c PriceChange) string { return c.Name })
}
