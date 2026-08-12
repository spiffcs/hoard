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
	// "BINDER" is the loose half as a section — every binder combined, whatever
	// each is named — so it stays a fixed label, not the default binder's name.
	t.AddStyled(env.Bold(), ui.C("BINDER"), ui.C(ui.Count(binder.TotalCopies)),
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
			// Identity pips beside the name; the first column to drop.
			{Title: ui.HeaderIdentity, Align: ui.Left, Priority: 6, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Style: env.Dim()},
			{Title: "COPIES", Align: ui.Right},
			// Capped: deck names run long, and without a ceiling this column
			// would squeeze the card name to its minimum.
			{Title: "HELD IN", Align: ui.Left, Flex: true, Min: 10, Max: 34,
				Priority: 5, Style: env.Dim()},
		},
	}
	var copies int
	for _, r := range rows {
		copies += r.Copies
		t.Add(ui.Cell{Text: r.Name, Style: env.Identity(r.ColorIdentity)},
			ui.C(ui.Pips(r.ColorIdentity)),
			ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
			ui.C(ui.FinishTreated(r.Finish, r.Treatment)), ui.C(ui.Count(r.Copies)), ui.C(r.HeldIn))
	}
	// Three agreements in one sentence, and the verb takes its number from
	// the copies rather than from the cards standing next to it.
	verb := "count"
	if copies == 1 {
		verb = "counts"
	}
	return t.Render() + env.Dim()(fmt.Sprintf(
		"\n%s across %s %s as $0.00.",
		ui.PluralCount(copies, "copy", "copies"),
		ui.PluralCount(len(rows), "card", "cards"), verb)) + "\n"
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

// Refused renders the price corrections in force: what hoard declined to
// report, and the figure it used instead.
//
// The refused figure is shown beside the substitute rather than replaced by
// it, because the correction is the claim being made and a claim the owner
// cannot check is worth very little. The multiple is what makes a row
// legible at a glance — "$0.56 → $97.55" only reads as absurd once you have
// done the division yourself.
func Refused(env ui.Env, rows []store.PriceOverrideRow) string {
	if len(rows) == 0 {
		return env.Dim()("No prices were refused; every figure agrees with the asks beside it.") + "\n"
	}
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 20},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Style: env.Dim()},
			{Title: "COPIES", Align: ui.Right, Priority: 5},
			{Title: "REFUSED", Align: ui.Right, Style: env.Dim()},
			{Title: "USING", Align: ui.Right},
			{Title: "UNDER BY", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Title: "SOURCE", Align: ui.Left, Priority: 6, Style: env.Dim()},
		},
	}
	for _, r := range rows {
		under := ""
		if r.Refused > 0 {
			under = fmt.Sprintf("%.0fx", r.Price/r.Refused)
		}
		t.Add(ui.C(r.Name),
			ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
			ui.C(r.Finish), ui.C(ui.Count(r.Quantity)),
			ui.C(ui.Money(r.Refused)), ui.C(ui.Money(r.Price)),
			ui.C(under), ui.C(r.Source))
	}
	return t.Render() + env.Dim()(fmt.Sprintf(
		"\n%s priced from the cheapest ask instead of a market price no sale stands behind.",
		ui.PluralCount(len(rows), "holding", "holdings"))) + "\n"
}
