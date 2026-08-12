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
// points away from, which keeps a dangling "→" out of a squeezed row. FROM
// outlives WAS, because a baseline date removed from beside the price it dates
// would leave that price looking like it came from the window's start.
func moversTable(env ui.Env, sections []moverSection, cutoff time.Time) ui.Table {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			// Identity pips beside the name, dropped before anything else on
			// a narrow terminal — meaning-bearing ornament, not data.
			{Title: ui.HeaderIdentity, Align: ui.Left, Priority: 7, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},
			// Where each row's own measurement starts, filled only on the rows
			// that start later than the window did. On a window the history
			// covers every cell is blank and the table drops the column, which
			// is why this costs nothing on the ordinary report.
			{Title: "FROM", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 1, Style: env.Dim()},
			{Title: "IMPACT", Align: ui.Right},
		},
	}

	// One scale across both sections: RISERS and SINKERS share the table,
	// and a diverging ramp is symmetric around zero by construction — the
	// extents come from the visible (already truncated) rows so the
	// endpoint colors actually appear.
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
			// A finish column reading "nonfoil" down every row is noise; the
			// foils are what want pointing out.
			finish := ui.FinishTreated(c.Finish, c.Treatment)
			// Each delta column fades on the diverging ramp against its own
			// visible extreme — the sign stays in the text, so piped output
			// loses nothing.
			changeStyle := env.Diverge(ui.DivergeFrac(c.Pct(), pctMax))
			impactStyle := env.Diverge(ui.DivergeFrac(c.TotalDelta(), impactMax))
			// The indent lives in the name cell, so every column to its right
			// stays aligned with the section heading above.
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

// Market lays out one section of the vendor comparison: its header line,
// then its rows. The three sections share a column shape so the tables stack
// without the eye having to re-find the numbers.
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
	// The name block every section shares: tinted name, printing, finish —
	// no pips column, the tint alone carries the identity in these dense
	// tables. A profit is the one genuine gain here; the other sections'
	// ratios stay uncolored — a below-market discount in red would read as
	// a loss when it is a reason to buy.
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
				ui.C(ui.Money(o.Market)), ui.C("last sold"),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.Cell{Text: "+" + ui.Money(o.Profit()), Style: env.Gain()})...)
		case market.KindLiquid:
			// The ratio columns grade on a color ramp — how close to the
			// section's ideal, not a gain/loss direction.
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

// Comps lays out the comp-sheet section: its header line, then one row
// per owned printing with every vendor's number — the market anchor, each
// ask, the buylist bid, and the spread between ask and bid graded so a
// tight (trustworthy) spread reads green.
func Comps(env ui.Env, comps []market.Comp) string {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},
			// The card furniture gives way before any vendor column — the
			// per-vendor numbers are this table's whole point.
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

// compMoney renders a vendor's figure, or the unknown dash when that
// vendor did not quote the card.
func compMoney(has bool, v float64) string {
	if !has {
		return "—"
	}
	return ui.Money(v)
}

// spreadCell grades a defined spread on the tight-is-green ramp; an
// undefined one (no buylist bid) renders the dash, dim. Zero and negative
// spreads render — a bid at or over the low ask is arbitrage, not a blank.
func spreadCell(env ui.Env, c market.Comp) ui.Cell {
	if !c.HasSpread() {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	s := c.Spread()
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.MarkupGrade(s))}
}

// Movers renders the risers and sinkers, and what they did to the hoard.
//
// window names the period in words ("since 29 Jun"), because prices are observed
// when a refresh runs rather than continuously. cutoff is the same moment as a
// time, and only the FROM column reads it; the zero time suits a caller with no
// cutoff to name, such as update-prices comparing against the last refresh.
//
// Both summary sentences name the population they counted over, not just the
// count, because a count alone cannot say whether a small number means a quiet
// hoard or a thin record. The population is every printing priced more than
// once: two prices are what a change is made of, and a printing seen exactly
// once is the one thing there is no way to compare. It is a narrow exclusion
// and saying so is what stops a reader reading a short list as a broken tool.
//
// The sentence used to name a different population — those priced by the cutoff
// — which was true while a printing without a price at the cutoff was dropped
// altogether. It is not true now: a window is a range, a record that starts
// inside it is measured across the part it has, and the count no longer falls
// away as the window widens past the record's start.
func Movers(env ui.Env, changes []store.PriceChange, limit int, window string, cutoff time.Time) string {
	if len(changes) == 0 {
		return env.Dim()("No price changes "+window+", among printings priced more than once.") + "\n"
	}
	// The net is the roll-up figure, so it holds out sets still settling after
	// release; the table above it does not, because a set's own movement is
	// real and worth reading. That gap is the reason the sentence names how
	// many sets it left out rather than printing a quieter number in silence.
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
