package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// DefaultMoverRows is how many risers and sinkers a section shows before it is
// truncated. Ten fits a terminal beside the rest of a refresh's output; the tail
// is what --limit is for.
const DefaultMoverRows = 10

// moverSection is one titled group of rows: the risers, or the sinkers.
type moverSection struct {
	Title string
	Rows  []store.PriceChange
}

// moverSections splits changes into the biggest risers and the biggest sinkers.
//
// Both are ordered by what the move is worth across every copy held rather than
// by the per-copy price change: fifty commons that each gained a dime moved the
// hoard more than one mythic that gained a dollar, and sorting on the sticker
// price buries that.
func moverSections(changes []store.PriceChange, limit int) []moverSection {
	if limit <= 0 {
		limit = DefaultMoverRows
	}
	return []moverSection{
		{"RISERS", topMovers(changes, limit,
			func(c store.PriceChange) bool { return c.TotalDelta() > 0 },
			func(a, b store.PriceChange) int { return cmp.Compare(b.TotalDelta(), a.TotalDelta()) })},
		{"SINKERS", topMovers(changes, limit,
			func(c store.PriceChange) bool { return c.TotalDelta() < 0 },
			func(a, b store.PriceChange) int { return cmp.Compare(a.TotalDelta(), b.TotalDelta()) })},
	}
}

// moversTable lays out both sections: what the card is, what it cost, what it
// costs now, and what that did to the hoard.
//
// One table rather than two so the halves share a column layout — separately, a
// five-figure sinker widens its price columns and the two tables print with
// different shapes above one another.
//
// Priorities give up columns least-useful-first, so the narrowest terminal keeps
// the card, its price now, and the impact. The arrow drops before the price it
// points away from, which keeps a dangling "→" out of a squeezed row.
func moversTable(env ui.Env, sections []moverSection) ui.Table {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 1, Style: env.Dim()},
			{Title: "IMPACT", Align: ui.Right},
		},
	}

	first := true
	for _, sec := range sections {
		if len(sec.Rows) == 0 {
			continue
		}
		if !first {
			t.AddSpacer()
		}
		first = false
		t.AddStyled(env.Bold(), ui.C(sec.Title))
		for _, c := range sec.Rows {
			// A finish column reading "nonfoil" down every row is noise; the
			// foils are what want pointing out.
			finish := ui.Finish(c.Finish)
			// The indent lives in the name cell, so every column to its right
			// stays aligned with the section heading above.
			t.Add(ui.C("  "+c.Name), ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(ui.Money(c.Old)), ui.C("→"), ui.C(ui.Money(c.New)),
				ui.C(ui.SignedPercent(c.Pct())), ui.C(ui.Qty(c.Copies)),
				ui.C(ui.SignedMoney(c.TotalDelta())))
		}
	}
	return t
}

// topMovers filters, sorts and truncates one section's rows.
//
// The sort is stable with a full tiebreak, for the same reason store's own
// listings pin ties: same-delta rows are the common case (every 1-copy card
// that moved the same cent), and truncating an unpinned order would make rows
// appear and disappear between two runs over identical data.
func topMovers(all []store.PriceChange, limit int, keep func(store.PriceChange) bool,
	order func(a, b store.PriceChange) int) []store.PriceChange {
	var out []store.PriceChange
	for _, c := range all {
		if keep(c) {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b store.PriceChange) int {
		if c := order(a, b); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		if c := strings.Compare(a.ScryfallID, b.ScryfallID); c != 0 {
			return c
		}
		return strings.Compare(a.Finish, b.Finish)
	})
	return out[:min(len(out), limit)]
}

// Arbitrage lays out one section of the vendor comparison. The three share a
// column shape so the tables stack without the eye having to re-find the numbers.
func Arbitrage(env ui.Env, sec arbitrage.Section) string {
	t := ui.Table{
		Env: env,
		Cols: []ui.Col{
			{Align: ui.Left, Flex: true, Min: 16},
			{Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Align: ui.Right},
			{Align: ui.Left, Style: env.Dim()},
			{Align: ui.Right},
			{Align: ui.Left, Style: env.Dim()},
			{Align: ui.Right},
		},
	}
	for _, o := range sec.Rows {
		finish := ui.Finish(o.Card.Finish)
		switch sec.Kind {
		case arbitrage.KindProfit:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.C("+"+ui.Money(o.Profit())))
		case arbitrage.KindLiquid:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.C(ui.Percent(o.Liquidity())))
		default:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.BuyAt)), ui.C(o.BuyFrom),
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.C("-"+ui.Percent(o.BelowMarket())))
		}
	}
	return t.Render()
}

// Movers renders the risers and sinkers, and what they did to the hoard.
//
// window names the period in words ("since 29 Jun"), because prices are observed
// when a refresh runs rather than continuously.
func Movers(env ui.Env, changes []store.PriceChange, limit int, window string) string {
	if len(changes) == 0 {
		return env.Dim()("No price changes "+window+".") + "\n"
	}
	var net float64
	for _, c := range changes {
		net += c.TotalDelta()
	}
	return moversTable(env, moverSections(changes, limit)).Render() + "\n" +
		env.Dim()(fmt.Sprintf("%s printings moved %s. Net change: %s",
			ui.Count(len(changes)), window, ui.SignedMoney(net))) + "\n"
}
