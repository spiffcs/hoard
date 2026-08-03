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

// marketSection is one table's run of the flat cursor space.
type marketSection struct{ start, count int }

// compsSection indexes the comps table in the three-section layout; the
// two below it are KindProfit and KindLiquid (BELOW MARKET left the
// browser — applyMarketRows guarantees its kind never reaches this file).
const compsSection = 2

// marketSections maps the three tables into flat cursor space: the Kind
// runs in m.marketRows — always grouped in Kind order, sortArbRows keeps
// that invariant — then the comps.
func (m Model) marketSections() [3]marketSection {
	var s [3]marketSection
	for _, r := range m.marketRows {
		s[r.Kind].count++
	}
	s[compsSection].count = len(m.marketComps)
	start := 0
	for i := range s {
		s[i].start = start
		start += s[i].count
	}
	return s
}

// marketCursorPos maps the flat cursor to (section, index within it).
func (m Model) marketCursorPos() (sec, idx int) {
	cur := min(max(m.cursor[paneCards], 0), max(m.marketTotalRows()-1, 0))
	secs := m.marketSections()
	for i := len(secs) - 1; i >= 0; i-- {
		if secs[i].count > 0 && cur >= secs[i].start {
			return i, min(cur-secs[i].start, secs[i].count-1)
		}
	}
	return 0, 0
}

// applyMarketComps derives the visible comp sheets from the given (already
// container-filtered) comps: top-N by value, then the same per-copy value
// floor the Kind rows get.
func (m *Model) applyMarketComps(all []market.Comp) {
	comps := market.TopComps(all, marketRowLimit)
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

// The comps sort cycles follow the visible side: every column its table
// shows and nothing else — "spread" means that side's spread, since
// sorting the sell table by the buy side's bid-spread shuffled rows
// against the column on screen (observed live). SPREAD leads as the
// default: the spread is the comp sheet's whole question.
var (
	compsSellSortColumns = []string{"spread", "name", "set/num", "fin", "tcg sold", "mp", "ck"}
	compsBuySortColumns  = []string{"spread", "name", "set/num", "fin", "tcg sold", "mp", "ck", "ck buylist"}
)

// compsSortColumnsNow is the cycle for the side currently showing.
func (m Model) compsSortColumnsNow() []string {
	if m.compsBuySide {
		return compsBuySortColumns
	}
	return compsSellSortColumns
}

// sortCompRows re-orders the comps by their own column and direction.
func (m *Model) sortCompRows() {
	key, rev := m.compsSortColumnsNow()[m.compsSortIdx], m.compsSortRev
	buySide := m.compsBuySide
	sortRows(m.marketComps, rev, func(a, b market.Comp) int {
		if c := compKeyFor(key, buySide, a, b); c != 0 {
			return c
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
}

// compKeyFor compares two comp sheets on one column. Money columns run
// descending (the repo's convention, unquoted vendors last for free);
// spread runs ascending — the tight-or-negative end is the interesting
// one — with undefined spreads last.
func compKeyFor(key string, buySide bool, a, b market.Comp) int {
	switch key {
	case "tcg sold":
		return cmp.Compare(b.Market, a.Market)
	case "mp":
		return cmp.Compare(b.Manapool, a.Manapool)
	case "ck":
		return cmp.Compare(b.CK, a.CK)
	case "ck buylist":
		return cmp.Compare(b.Buylist, a.Buylist)
	case "name":
		return strings.Compare(a.Card.Name, b.Card.Name)
	case "set/num":
		return comparePrinting(a.Card.SetCode, a.Card.CollectorNumber,
			b.Card.SetCode, b.Card.CollectorNumber)
	case "fin":
		return strings.Compare(a.Card.Finish, b.Card.Finish)
	}
	// "spread", and the default: the side's own spread.
	if buySide {
		return cmp.Compare(spreadOrInf(a), spreadOrInf(b))
	}
	return cmp.Compare(saleSpreadOrInf(a), saleSpreadOrInf(b))
}

// spreadOrInf sorts the rows with no bid spread after every defined one.
func spreadOrInf(c market.Comp) float64 {
	if !c.HasSpread() {
		return math.Inf(1)
	}
	return c.Spread()
}

// saleSpreadOrInf does the same for the sell side's sale spread.
func saleSpreadOrInf(c market.Comp) float64 {
	s, ok := c.SaleSpread()
	if !ok {
		return math.Inf(1)
	}
	return s
}

// compsSellNote and compsBuyNote describe each side of the sheet; TCG
// SOLD names its own source. The CLI keeps market.CompsNote for its one
// full-width table.
const (
	compsSellNote = "vendor sale prices; spread is how much they disagree"
	compsBuyNote  = "the cash bid against the asks"
)

// compsSectionTable lays out the comps rows, headers included. The sheet
// has two halves and the table shows one at a time. The sell side is the
// sale-price comp: what each vendor sells the card for — tcgplayer's
// last-sold, the asks — with SPREAD measuring how much they disagree
// (agreement is what makes a price real). The buy side is the other side
// of the counter: the asks beside Card Kingdom's cash bid, with SPREAD as
// the buyer's haircut — buy at the ask, recoup at the bid.
func compsSectionTable(env ui.Env, comps []market.Comp, buySide bool) ui.Table {
	name := ui.Col{Title: "NAME", Align: ui.Left, Flex: true, Min: 10}
	setNum := ui.Col{Title: "SET/NUM", Align: ui.Left, Priority: 8, Style: env.Dim()}
	fin := ui.Col{Title: "FIN", Align: ui.Left, Priority: 7, Style: env.Dim()}

	if buySide {
		t := ui.Table{Cols: []ui.Col{name, setNum, fin,
			{Title: "TCG SOLD", Align: ui.Right},
			{Title: "MP", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "CK", Align: ui.Right, Priority: 5, Style: env.Dim()},
			{Title: "CK BUYLIST", Align: ui.Right},
			{Title: "SPREAD", Align: ui.Right},
		}}
		for _, c := range comps {
			t.Add(ui.Cell{Text: c.Card.Name, Style: env.Identity(c.Card.ColorIdentity)},
				ui.C(c.Printing()), ui.C(ui.Finish(c.Card.Finish)),
				ui.C(compMoney(c.HasMarket, c.Market)),
				ui.C(compMoney(c.HasManapool, c.Manapool)),
				ui.C(compMoney(c.HasCK, c.CK)),
				ui.C(compMoney(c.HasBuylist, c.Buylist)),
				compSpreadCell(env, c))
		}
		return t
	}

	t := ui.Table{Cols: []ui.Col{name, setNum, fin,
		{Title: "TCG SOLD", Align: ui.Right},
		{Title: "MP", Align: ui.Right, Priority: 6, Style: env.Dim()},
		{Title: "CK", Align: ui.Right, Priority: 5, Style: env.Dim()},
		{Title: "SPREAD", Align: ui.Right},
	}}
	for _, c := range comps {
		t.Add(ui.Cell{Text: c.Card.Name, Style: env.Identity(c.Card.ColorIdentity)},
			ui.C(c.Printing()), ui.C(ui.Finish(c.Card.Finish)),
			ui.C(compMoney(c.HasMarket, c.Market)),
			ui.C(compMoney(c.HasManapool, c.Manapool)),
			ui.C(compMoney(c.HasCK, c.CK)),
			compSaleSpreadCell(env, c))
	}
	return t
}

// compSaleSpreadCell heats the sale prices' disagreement — green when the
// vendors agree, darkening red as they don't — or the dim dash when the
// sheet has fewer than two sale figures to compare.
func compSaleSpreadCell(env ui.Env, c market.Comp) ui.Cell {
	s, ok := c.SaleSpread()
	if !ok {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.SaleSpreadGrade(s))}
}

// compMoney renders a vendor's figure, or the unknown dash when that
// vendor did not quote the card.
func compMoney(has bool, v float64) string {
	if !has {
		return "—"
	}
	return ui.Money(v)
}

// compSpreadCell heats a defined spread on the markup ramp — negative
// green (the bid at or over the ask is the sheet's best news), reddening
// toward 100%; an undefined one renders the dash, dim.
func compSpreadCell(env ui.Env, c market.Comp) ui.Cell {
	if !c.HasSpread() {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	s := c.Spread()
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.MarkupGrade(s))}
}
