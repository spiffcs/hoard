package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const DefaultMoverRows = 10

type moverSection struct {
	Title string
	Rows  []store.PriceChange
}

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

func moversTable(env ui.Env, sections []moverSection, cutoff time.Time) ui.Table {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},

			{Title: ui.HeaderIdentity, Align: ui.Left, Priority: 7, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},

			{Title: "FROM", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 1, Style: env.Dim()},
			{Title: "IMPACT", Align: ui.Right},
		},
	}

	var pctMax, impactMax float64
	for _, sec := range sections {
		p, i := store.MoverExtents(sec.Rows)
		pctMax = max(pctMax, p)
		impactMax = max(impactMax, i)
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

			finish := ui.FinishTreated(c.Finish, c.Treatment)

			changeStyle := env.Diverge(ui.DivergeFrac(c.Pct(), pctMax))
			impactStyle := env.Diverge(ui.DivergeFrac(c.TotalDelta(), impactMax))

			t.Add(ui.Cell{Text: "  " + c.Name, Style: env.Identity(c.ColorIdentity)},
				ui.C(ui.Pips(c.ColorIdentity)),
				ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(c.BaselineFrom(cutoff)),
				ui.C(ui.Money(c.Old)), ui.C("→"), ui.C(ui.Money(c.New)),
				ui.Cell{Text: ui.SignedPercent(c.Pct()), Style: changeStyle}, ui.C(ui.Qty(c.Copies)),
				ui.Cell{Text: ui.SignedMoney(c.TotalDelta()), Style: impactStyle})
		}
	}
	return t
}

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
		return strings.Compare(a.Finish.String(), b.Finish.String())
	})
	return out[:min(len(out), limit)]
}

func Market(env ui.Env, sec market.Section) string {
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

	cardCells := func(o market.Opportunity) []ui.Cell {
		return []ui.Cell{
			{Text: o.Card.Name, Style: env.Identity(o.Card.ColorIdentity)},
			ui.C(o.Printing()), ui.C(ui.FinishTreated(o.Card.Finish, o.Card.Treatment)),
		}
	}
	for _, o := range sec.Rows {
		switch sec.Kind {
		case market.KindProfit:
			t.Add(append(cardCells(o),
				ui.C(ui.Money(o.BuyAt)), ui.C(o.BuyFrom),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.Cell{Text: "+" + ui.Money(o.Profit()), Style: env.Gain()})...)
		case market.KindLiquid:

			t.Add(append(cardCells(o),
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.Cell{Text: ui.Percent(o.Liquidity()),
					Style: env.Grade(market.LiquidityGrade(o.Liquidity()))})...)
		default:
			t.Add(append(cardCells(o),
				ui.C(ui.Money(o.BuyAt)), ui.C(o.BuyFrom),
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.Cell{Text: "-" + ui.Percent(o.BelowMarket()),
					Style: env.Grade(market.BelowMarketGrade(o.BelowMarket()))})...)
		}
	}
	return env.Bold()(sec.Kind.Title()) + env.Dim()("  "+sec.Kind.Note()) + "\n" + t.Render()
}

func Comps(env ui.Env, comps []market.Comp) string {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},

			{Title: "SET/NUM", Align: ui.Left, Priority: 8, Style: env.Dim()},
			{Title: "FIN", Align: ui.Left, Priority: 7, Style: env.Dim()},
			{Title: "MP", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "CK", Align: ui.Right, Priority: 5, Style: env.Dim()},
			{Title: "AT", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "MARKET", Align: ui.Right},
			{Title: "LOW", Align: ui.Right},
			{Title: "BUYLIST", Align: ui.Right},
			{Title: "SPREAD", Align: ui.Right},
		},
	}
	for _, c := range comps {
		t.Add(ui.Cell{Text: c.Card.Name, Style: env.Identity(c.Card.ColorIdentity)},
			ui.C(c.Printing()), ui.C(ui.FinishTreated(c.Card.Finish, c.Card.Treatment)),
			ui.C(compMoney(c.HasManapool, c.Manapool)),
			ui.C(compMoney(c.HasCK, c.CK)),
			ui.C(c.LowFrom),
			ui.C(compMoney(c.HasMarket, c.Market)),
			ui.C(ui.Money(c.Low)),
			ui.C(compMoney(c.HasBuylist, c.Buylist)),
			spreadCell(env, c))
	}
	return env.Bold()(market.CompsTitle) + env.Dim()("  "+market.CompsNote) + "\n" + t.Render()
}

func compMoney(has bool, v float64) string {
	if !has {
		return "—"
	}
	return ui.Money(v)
}

func spreadCell(env ui.Env, c market.Comp) ui.Cell {
	if !c.HasSpread() {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	s := c.Spread()
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.MarkupGrade(s))}
}

func Movers(env ui.Env, changes []store.PriceChange, limit int, window string, cutoff time.Time) string {
	if len(changes) == 0 {
		return env.Dim()("No price changes "+window+", among printings priced more than once.") + "\n"
	}

	net, heldOut := store.NetMoved(changes, time.Now())
	settling := ""
	if heldOut > 0 {
		settling = fmt.Sprintf(", holding out %s released in the last %d days",
			ui.PluralCount(heldOut, "set", "sets"), store.SettlingDays())
	}
	return moversTable(env, moverSections(changes, limit), cutoff).Render() + "\n" +
		env.Dim()(fmt.Sprintf("%s printings moved %s, among those priced more than once. Net change: %s%s",
			ui.Count(len(changes)), window, ui.SignedMoney(net), settling)) + "\n"
}
