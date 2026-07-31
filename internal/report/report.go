// Package report renders hoard's data as text.
//
// It sits between the store and internal/ui: ui knows how to lay out a grid but
// nothing about cards, and the commands know what the user asked for but should
// not also own column priorities. Everything here is a pure function from
// domain values to a string, so any table can be checked at any terminal width
// without a database or a terminal.
package report

import (
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// barCells is the width of the summary's share-bar column.
const barCells = 10

// Summary is the hoard's totals: the binder, the decks, and the grand total.
//
// Two labelled sections rather than a flat list distinguished by a repeated
// "Deck: " prefix.
func Summary(env ui.Env, binder store.CollectionTotals, decks []store.DeckSummary) string {
	sorted := store.DecksByValue(decks)

	var deckCopies int
	var deckValue float64
	for _, d := range sorted {
		deckCopies += d.TotalCopies
		deckValue += d.Value
	}
	grand := binder.Value + deckValue

	// Shares are fractions of the grand total, so the two section bars tile the
	// column and calibrate the deck bars below them.
	share := func(v float64) string {
		if !env.Bars || grand <= 0 {
			return ""
		}
		return ui.Bar(v/grand, barCells)
	}

	t := ui.Table{
		Env: env,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "CARDS", Align: ui.Right, Priority: 2},
			{Title: "VALUE", Align: ui.Right},
			{Align: ui.Left, Min: 6, Max: barCells, Priority: 3, Style: env.Dim()},
		},
	}

	// The section rows are bold throughout, bars included: the two of them tile
	// the bar column and so double as the scale legend for the deck rows below.
	t.AddStyled(env.Bold(), ui.C(strings.ToUpper(store.LooseName)), ui.C(ui.Count(binder.TotalCopies)),
		ui.C(ui.Money(binder.Value)), ui.C(share(binder.Value)))
	t.AddStyled(env.Bold(), ui.C(fmt.Sprintf("DECKS · %d", len(sorted))),
		ui.C(ui.Count(deckCopies)), ui.C(ui.Money(deckValue)), ui.C(share(deckValue)))

	if len(sorted) > 0 {
		t.AddSpacer()
		for _, d := range sorted {
			// The indent is part of the name cell, so every column to its right
			// stays aligned with the section rows above.
			t.Add(ui.C("  "+d.Name), ui.C(ui.Count(d.TotalCopies)), ui.C(ui.Money(d.Value)),
				ui.C(share(d.Value)))
		}
	}

	t.AddSpacer()
	// The total's bar cell is empty: a full bar there is ink for no information.
	t.AddStyled(env.Bold(), ui.C("TOTAL"), ui.C(ui.Count(binder.TotalCopies+deckCopies)),
		ui.C(ui.Money(grand)), ui.C(""))
	return t.Render()
}

// Unpriced lists the holdings nothing can price, and what to do about them.
func Unpriced(env ui.Env, rows []store.UnpricedRow) string {
	if len(rows) == 0 {
		return env.Dim()("Every card you own has a price.") + "\n"
	}
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 20},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Style: env.Dim()},
			{Title: "COPIES", Align: ui.Right},
			// Capped and dropped first: deck names run long, and without a
			// ceiling this column would squeeze the card name to its minimum.
			{Title: "HELD IN", Align: ui.Left, Flex: true, Min: 10, Max: 34,
				Priority: 5, Style: env.Dim()},
		},
	}
	var copies int
	for _, r := range rows {
		copies += r.Copies
		t.Add(ui.C(r.Name), ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
			ui.C(ui.Finish(r.Finish)), ui.C(ui.Count(r.Copies)), ui.C(r.HeldIn))
	}
	return t.Render() + env.Dim()(fmt.Sprintf(
		"\n%s copies across %s cards count as $0.00.",
		ui.Count(copies), ui.Count(len(rows)))) + "\n"
}

// UnpricedAdvice is what to do about unpriced holdings — separate from the
// table so a machine-readable rendering of the data never carries prose, and
// the caller decides where advice belongs.
//
// Two different cures, and the less obvious one is usually the answer: a card
// stored in a finish its printing does not come in can never be priced,
// however many times you refresh.
func UnpricedAdvice(env ui.Env) string {
	return env.Dim()("Try: hoard repair-finishes, then hoard update-prices") + "\n"
}
