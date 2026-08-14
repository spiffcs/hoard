package store

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

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

func CollectionByValue(rows []CollectionRow) []CollectionRow {
	return byValue(rows,
		func(r CollectionRow) float64 { return r.Value },
		func(r CollectionRow) string { return r.Name })
}

func DecksByValue(decks []DeckSummary) []DeckSummary {
	return byValue(decks,
		func(d DeckSummary) float64 { return d.Value },
		func(d DeckSummary) string { return d.Name })
}

func EntriesByValue(entries []EntryView) []EntryView {
	return byValue(entries,
		func(e EntryView) float64 { return e.Value() },
		func(e EntryView) string { return e.Card.Name })
}

func MoversByImpact(changes []PriceChange) []PriceChange {
	return byValue(changes,
		func(c PriceChange) float64 { return math.Abs(c.TotalDelta()) },
		func(c PriceChange) string { return c.Name })
}
