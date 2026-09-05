package report

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const barCells = 10

func Summary(env ui.Env, binder store.CollectionTotals, decks []store.DeckSummary) string {
	sorted := store.DecksByValue(decks)

	var deckCopies int
	var deckValue float64
	var uncounted bool
	for _, d := range sorted {
		if !d.Counted {
			uncounted = true
			continue
		}
		deckCopies += d.TotalCopies
		deckValue += d.Value
	}
	grand := binder.Value + deckValue

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

	t.AddStyled(env.Bold(), ui.C("BINDER"), ui.C(ui.Count(binder.TotalCopies)),
		ui.C(ui.Money(binder.Value)), ui.C(share(binder.Value)))
	t.AddStyled(env.Bold(), ui.C(fmt.Sprintf("DECKS · %d", len(sorted))),
		ui.C(ui.Count(deckCopies)), ui.C(ui.Money(deckValue)), ui.C(share(deckValue)))

	if len(sorted) > 0 {
		t.AddSpacer()
		for _, d := range sorted {

			name, bar := "  "+d.Name, share(d.Value)
			if !d.Counted {
				name, bar = name+" *", ""
			}
			t.Add(ui.C(name), ui.C(ui.Count(d.TotalCopies)), ui.C(ui.Money(d.Value)),
				ui.C(bar))
		}
	}

	t.AddSpacer()

	t.AddStyled(env.Bold(), ui.C("TOTAL"), ui.C(ui.Count(binder.TotalCopies+deckCopies)),
		ui.C(ui.Money(grand)), ui.C(""))
	out := t.Render()
	if uncounted {
		out += env.Dim()("* not counted toward your collection") + "\n"
	}
	return out
}

func Unpriced(env ui.Env, rows []store.UnpricedRow) string {
	if len(rows) == 0 {
		return env.Dim()("Every card you own has a price.") + "\n"
	}
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 20},

			{Title: ui.HeaderIdentity, Align: ui.Left, Priority: 6, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Style: env.Dim()},
			{Title: "COPIES", Align: ui.Right},

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

	verb := "count"
	if copies == 1 {
		verb = "counts"
	}
	return t.Render() + env.Dim()(fmt.Sprintf(
		"\n%s across %s %s as $0.00.",
		ui.PluralCount(copies, "copy", "copies"),
		ui.PluralCount(len(rows), "card", "cards"), verb)) + "\n"
}

func UnpricedAdvice(env ui.Env) string {
	return env.Dim()("Try: hoard misfinished, then hoard update-prices") + "\n"
}

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
			ui.C(r.Finish.String()), ui.C(ui.Count(r.Quantity)),
			ui.C(ui.Money(r.Refused)), ui.C(ui.Money(r.Price)),
			ui.C(under), ui.C(r.Source))
	}
	return t.Render() + env.Dim()(fmt.Sprintf(
		"\n%s priced from the cheapest ask instead of a market price no sale stands behind.",
		ui.PluralCount(len(rows), "holding", "holdings"))) + "\n"
}
