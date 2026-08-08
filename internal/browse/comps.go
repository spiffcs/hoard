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

// marketTotalRows is every data row on the market view: the three Kind
// sections, then the comps. This is the count the header reports and the
// emptiness the pane tests — not the cursor's range, which is wider.
func (m Model) marketTotalRows() int {
	return len(m.marketRows) + len(m.marketComps)
}

// marketCursorSlots is how far the cursor can travel, which exceeds the row
// count by one slot for every empty table — see marketSection.
func (m Model) marketCursorSlots() int {
	var n int
	for _, s := range m.marketSections() {
		n += s.span
	}
	return n
}

// selectedComp is the comp sheet under the cursor, nil when the cursor sits
// in the Kind sections or on a heading with no rows under it.
func (m Model) selectedComp() *market.Comp {
	sec, idx := m.marketCursorPos()
	if sec != compsSection || idx >= len(m.marketComps) {
		return nil
	}
	return &m.marketComps[idx]
}

// marketSection is one table's place in the two spaces the market view
// keeps at once. rowStart and count index the data — m.marketRows for the
// Kind sections, m.marketComps for the last. curStart and span place the
// table in the flat cursor space, and the two diverge because an empty
// table still takes one cursor slot, for its heading.
//
// That slot is the point: each table owns keys of its own — 'b' flips a
// side or a band, 's' sorts — and a table the cursor cannot reach is a
// table whose keys have nowhere to land. It is also the only honest way to
// reach the buylist table's other band when the band on show is empty,
// which is exactly when the other one is worth a look.
type marketSection struct {
	rowStart, count int
	curStart, span  int
}

// compsSection indexes the comps table in the three-section layout; the
// two below it are KindProfit and KindLiquid (BELOW MARKET left the
// browser — applyMarketRows guarantees its kind never reaches this file).
const compsSection = 2

// marketSections maps the three tables into both spaces: the Kind runs in
// m.marketRows — always grouped in Kind order, sortArbRows keeps that
// invariant — then the comps.
func (m Model) marketSections() [3]marketSection {
	var s [3]marketSection
	for _, r := range m.marketRows {
		// Total by construction, not by upstream filtering alone: a stray
		// Kind (KindLowball is 3) indexing the [3] array is a panic in the
		// middle of a frame, and the filter guarding it lives a file away.
		if r.Kind < 0 || int(r.Kind) >= compsSection {
			continue
		}
		s[r.Kind].count++
	}
	s[compsSection].count = len(m.marketComps)
	row, cur := 0, 0
	for i := range s {
		s[i].rowStart, s[i].curStart = row, cur
		s[i].span = max(s[i].count, 1)
		row += s[i].count
		cur += s[i].span
	}
	return s
}

// marketCursorPos maps the flat cursor to (section, index within it). On an
// empty table the index is 0 and addresses no row: callers that read data
// must check the section's count, which is what selectedComp and
// selectedMarketRow do.
func (m Model) marketCursorPos() (sec, idx int) {
	cur := min(max(m.cursor[paneCards], 0), max(m.marketCursorSlots()-1, 0))
	secs := m.marketSections()
	for i := len(secs) - 1; i >= 0; i-- {
		if cur >= secs[i].curStart {
			return i, min(cur-secs[i].curStart, max(secs[i].count-1, 0))
		}
	}
	return 0, 0
}

// firstMarketCursor is where the cursor belongs after the rows underneath
// change: the first table that actually has something in it. Landing on an
// empty heading is a place the user can choose to go, never one the view
// drops them into.
func (m Model) firstMarketCursor() int {
	for _, s := range m.marketSections() {
		if s.count > 0 {
			return s.curStart
		}
	}
	return 0
}

// selectedMarketRow is the Kind-section row under the cursor, nil on a comp
// sheet or on a heading with no rows under it.
func (m Model) selectedMarketRow() *market.Row {
	sec, idx := m.marketCursorPos()
	secs := m.marketSections()
	if sec == compsSection || secs[sec].count == 0 {
		return nil
	}
	return &m.marketRows[secs[sec].rowStart+idx]
}

// applyMarketComps derives the full comp ranking from the given (already
// container-filtered) comps: everything, value-ranked, after the same
// per-copy value floor the Kind rows get. Paging owns the truncation.
func (m *Model) applyMarketComps(all []market.Comp) {
	comps := market.TopComps(all, 0)
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
	m.marketAllComps = comps
	m.sortCompRows()
}

// The comps sort cycles follow the visible side: every column its table
// shows and nothing else — each side's derived column means that side's
// number, since sorting the sell table by the buy side's bid-spread
// shuffled rows against the column on screen (observed live). The derived
// column leads as the default: it is the comp sheet's whole question. The
// sell side calls its number PRICE DISPERSION (vendor sale prices
// disagreeing); SPREAD stays the buy side's word, where it really is
// bid-ask.
var (
	compsSellSortColumns = []string{"price dispersion", "name", "set/num", "fin", "tcg sold", "mp", "ck"}
	compsBuySortColumns  = []string{"spread", "name", "set/num", "fin", "tcg sold", "mp", "ck", "ck buylist"}
)

// compsSortColumnsNow is the cycle for the side currently showing.
func (m Model) compsSortColumnsNow() []string {
	if m.compsBuySide {
		return compsBuySortColumns
	}
	return compsSellSortColumns
}

// compsSortKey is the active column's name, index clamped at the read: the
// buy cycle is one column longer than the sell cycle, and the pairing that
// keeps compsSortIdx in range is the side flip's reset — a convention held
// in another file, not an invariant. A missed reset degrades to the last
// column instead of indexing past the slice.
func (m Model) compsSortKey() string {
	cols := m.compsSortColumnsNow()
	return cols[min(max(m.compsSortIdx, 0), len(cols)-1)]
}

// sortCompRows re-orders the full comp ranking by its own column and
// direction, then re-derives the visible page — a sort speaks for the
// whole ranking, so the page shows its slice of the new order.
func (m *Model) sortCompRows() {
	key, rev := m.compsSortKey(), m.compsSortRev
	buySide := m.compsBuySide
	sortRows(m.marketAllComps, rev, func(a, b market.Comp) int {
		if c := compKeyFor(key, buySide, a, b); c != 0 {
			return c
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
	m.deriveMarketPages()
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
	// "spread" / "price dispersion", and the default: the side's own
	// derived number.
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
	compsSellNote = "vendor sale prices"
	compsBuyNote  = "the cash bid against the asks"
)

// compsSectionTable lays out the comps rows, headers included. The sheet
// has two halves and the table shows one at a time. The sell side is the
// sale-price comp: what each vendor sells the card for — tcgplayer's
// last-sold, the asks — with PRICE DISPERSION measuring how much they
// disagree (agreement is what makes a price real; this is not a bid-ask
// spread, hence the word). The buy side is the other side of the counter:
// the asks beside Card Kingdom's cash bid, with SPREAD as the buyer's
// haircut — a true bid-ask gap, so the word stays.
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
				ui.C(c.Printing()), ui.C(ui.FinishTreated(c.Card.Finish, c.Card.Treatment)),
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
		{Title: "PRICE DISPERSION", Align: ui.Right},
	}}
	for _, c := range comps {
		t.Add(ui.Cell{Text: c.Card.Name, Style: env.Identity(c.Card.ColorIdentity)},
			ui.C(c.Printing()), ui.C(ui.FinishTreated(c.Card.Finish, c.Card.Treatment)),
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
