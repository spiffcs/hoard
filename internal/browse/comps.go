package browse

// The market view's COMPS section: per-vendor comp sheets rendered after
// the three Kind tables. Comps rows live in their own slice with their own
// sort state — a comp is not an Opportunity and has no Kind — and the flat
// cursor spans both, with the two helpers here as the single source of the
// boundary arithmetic.

import (
	"cmp"
	"math"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/ui"
)

// marketTotalRows is every data row the market view's cursor can visit:
// the three Kind sections, then the comps.
func (m Model) marketTotalRows() int {
	return len(m.marketRows) + len(m.marketComps)
}

// selectedComp is the comp sheet under the cursor, nil when the cursor
// sits in the Kind sections (or out of range).
func (m Model) selectedComp() *market.Comp {
	i := m.cursor[paneCards] - len(m.marketRows)
	if i < 0 || i >= len(m.marketComps) {
		return nil
	}
	return &m.marketComps[i]
}

// applyMarketComps derives the visible comp sheets from the last result:
// top-N by value, then the same per-copy value floor the Kind rows get.
func (m *Model) applyMarketComps() {
	comps := market.TopComps(m.marketResult.Comps, marketRowLimit)
	if min := m.floorMin(); min > 0 {
		kept := comps[:0]
		for _, c := range comps {
			unit := c.Card.Value
			if c.Card.Copies > 1 {
				unit /= float64(c.Card.Copies)
			}
			if unit >= min {
				kept = append(kept, c)
			}
		}
		comps = kept
	}
	m.marketComps = comps
	m.sortCompRows()
}

// compsSortColumns is the comps table's cycle; index 0 reproduces the
// value ranking rows arrive in.
var compsSortColumns = []string{"value", "spread", "market", "low", "buylist", "name", "set/num"}

// sortCompRows re-orders the comps by their own column and direction.
func (m *Model) sortCompRows() {
	key, rev := compsSortColumns[m.compsSortIdx], m.compsSortRev
	sortRows(m.marketComps, rev, func(a, b market.Comp) int {
		if c := compKeyFor(key, a, b); c != 0 {
			return c
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
}

// compKeyFor compares two comp sheets on one column. Money and value run
// descending (the repo's convention); spread runs ascending — the tight
// end is the interesting one — with undefined spreads last.
func compKeyFor(key string, a, b market.Comp) int {
	switch key {
	case "spread":
		return cmp.Compare(spreadOrInf(a), spreadOrInf(b))
	case "market":
		return cmp.Compare(b.Market, a.Market)
	case "low":
		return cmp.Compare(b.Low, a.Low)
	case "buylist":
		return cmp.Compare(b.Buylist, a.Buylist)
	case "name":
		return strings.Compare(a.Card.Name, b.Card.Name)
	case "set/num":
		return comparePrinting(a.Card.SetCode, a.Card.CollectorNumber,
			b.Card.SetCode, b.Card.CollectorNumber)
	}
	return cmp.Compare(b.Card.Value, a.Card.Value)
}

// spreadOrInf sorts the rows with no spread after every defined one.
func spreadOrInf(c market.Comp) float64 {
	if !c.HasSpread() {
		return math.Inf(1)
	}
	return c.Spread()
}

// compsSectionTable lays out the comps rows, headers included — the
// browse-side twin of report.Comps, sharing its column shape.
func compsSectionTable(env ui.Env, comps []market.Comp) ui.Table {
	t := ui.Table{Cols: []ui.Col{
		{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
		{Title: "SET/NUM", Align: ui.Left, Priority: 8, Style: env.Dim()},
		{Title: "FIN", Align: ui.Left, Priority: 7, Style: env.Dim()},
		{Title: "MP", Align: ui.Right, Priority: 6, Style: env.Dim()},
		{Title: "CK", Align: ui.Right, Priority: 5, Style: env.Dim()},
		{Title: "AT", Align: ui.Left, Priority: 4, Style: env.Dim()},
		{Title: "MARKET", Align: ui.Right},
		{Title: "LOW", Align: ui.Right},
		{Title: "BUYLIST", Align: ui.Right},
		{Title: "SPREAD", Align: ui.Right},
	}}
	for _, c := range comps {
		t.Add(ui.Cell{Text: c.Card.Name, Style: env.Identity(c.Card.ColorIdentity)},
			ui.C(c.Printing()), ui.C(ui.Finish(c.Card.Finish)),
			ui.C(compMoney(c.HasManapool, c.Manapool)),
			ui.C(compMoney(c.HasCK, c.CK)),
			ui.C(c.LowFrom),
			ui.C(compMoney(c.HasMarket, c.Market)),
			ui.C(ui.Money(c.Low)),
			ui.C(compMoney(c.HasBuylist, c.Buylist)),
			compSpreadCell(env, c))
	}
	return t
}

// compMoney renders a vendor's figure, or the unknown dash when that
// vendor did not quote the card.
func compMoney(has bool, v float64) string {
	if !has {
		return "—"
	}
	return ui.Money(v)
}

// compSpreadCell grades a defined spread tight-is-green; an undefined one
// renders the dash, dim.
func compSpreadCell(env ui.Env, c market.Comp) ui.Cell {
	if !c.HasSpread() {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	s := c.Spread()
	return ui.Cell{Text: ui.Percent(s), Style: env.Grade(market.SpreadGrade(s))}
}
