package browse

// Per-view sorting. Each right-pane view has its own columns, so each keeps
// its own sort: "s" cycles the focused view's columns, "S" reverses the
// current one, and switching views never disturbs an order already chosen.

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

// sortColumns is each view's sortable columns, in cycle order, named as their
// headers read. Index 0 is the default and reproduces the order rows already
// arrive in, so an untouched pane looks exactly as it always has.
//
// The market view is absent here: its three tables each keep their own sort
// (marketSortColumns), so its entry is a placeholder that only sizes the
// per-view state arrays.
var sortColumns = [...][]string{
	viewHoldings: {"value", "name", "set", "finish", "qty", "price"},
	viewMovers:   {"impact", "name", "set/num", "was", "now", "change", "qty"},
	viewUnpriced: {"name", "set/num", "finish", "qty", "held in"},
	viewWatches:  {"state", "name", "watch", "price"},
	viewMarket:   {"per-table"},
}

// marketSortColumns is each market table's cycle, indexed by market.Kind.
// Index 0 is that table's own ranking metric, reproducing arrival order.
var marketSortColumns = [...][]string{
	market.KindProfit:      {"profit", "name", "set/num", "last sold", "buylist", "to"},
	market.KindLiquid:      {"pays", "name", "set/num", "last sold", "buylist", "to"},
	market.KindBelowMarket: {"below", "name", "set/num", "ask", "at", "last sold"},
}

// selectedMarketKind is the table the cursor is in, defaulting to the first.
func (m Model) selectedMarketKind() market.Kind {
	if i := m.cursor[paneCards]; i >= 0 && i < len(m.marketRows) {
		return m.marketRows[i].Kind
	}
	return market.KindProfit
}

// firstMarketRowOfKind is where a table starts in the flat row list.
func (m Model) firstMarketRowOfKind(k market.Kind) int {
	for i, r := range m.marketRows {
		if r.Kind == k {
			return i
		}
	}
	return 0
}

// sortLabel is how the status line describes the focused view's order. On
// the market view it names the table the sort belongs to, since each keeps
// its own.
func (m Model) sortLabel() string {
	if m.view == viewMarket {
		// Checked before selectedMarketKind, which defaults to the profit
		// table for any cursor outside the Kind sections.
		if m.selectedComp() != nil {
			label := "comps · " + compsSortColumns[m.compsSortIdx]
			if m.compsSortRev {
				label += " (reversed)"
			}
			return label
		}
		k := m.selectedMarketKind()
		label := k.String() + " · " + marketSortColumns[k][m.marketSortIdx[k]]
		if m.marketSortRev[k] {
			label += " (reversed)"
		}
		return label
	}
	label := sortColumns[m.view][m.sortIdx[m.view]]
	if m.sortRev[m.view] {
		label += " (reversed)"
	}
	return label
}

// cycleSort moves the current view to its next column, forward order. On
// the market view only the cursor's table advances, and the cursor lands on
// that table's first row so the sort's effect is on screen.
func (m *Model) cycleSort() {
	if m.view == viewMarket {
		if m.selectedComp() != nil {
			m.compsSortIdx = (m.compsSortIdx + 1) % len(compsSortColumns)
			m.compsSortRev = false
			m.sortCompRows()
			m.cursor[paneCards] = len(m.marketRows) // the comps section's first row
			return
		}
		k := m.selectedMarketKind()
		m.marketSortIdx[k] = (m.marketSortIdx[k] + 1) % len(marketSortColumns[k])
		m.marketSortRev[k] = false
		m.sortArbRows()
		m.cursor[paneCards] = m.firstMarketRowOfKind(k)
		return
	}
	v := m.view
	m.sortIdx[v] = (m.sortIdx[v] + 1) % len(sortColumns[v])
	m.sortRev[v] = false
	m.applySort()
}

// reverseSort flips the current column's direction — on the market view,
// the cursor's table's column.
func (m *Model) reverseSort() {
	if m.view == viewMarket {
		if m.selectedComp() != nil {
			m.compsSortRev = !m.compsSortRev
			m.sortCompRows()
			m.cursor[paneCards] = len(m.marketRows)
			return
		}
		k := m.selectedMarketKind()
		m.marketSortRev[k] = !m.marketSortRev[k]
		m.sortArbRows()
		m.cursor[paneCards] = m.firstMarketRowOfKind(k)
		return
	}
	m.sortRev[m.view] = !m.sortRev[m.view]
	m.applySort()
}

// applySort re-orders the current view's rows in place.
func (m *Model) applySort() {
	key, rev := sortColumns[m.view][m.sortIdx[m.view]], m.sortRev[m.view]
	switch m.view {
	case viewWatches:
		sortRows(m.watches, rev, watchCompare(key))
	case viewMovers:
		sortRows(m.movers, rev, moverCompare(key))
	case viewUnpriced:
		sortRows(m.unpriced, rev, unpricedCompare(key))
	case viewMarket:
		m.sortArbRows()
	default:
		m.sortHoldings()
		m.applyFilter()
	}
}

// sortHoldings orders the card pane by the holdings view's own state, whatever
// view is currently showing — the left pane can reload cards from any view.
func (m *Model) sortHoldings() {
	key, rev := sortColumns[viewHoldings][m.sortIdx[viewHoldings]], m.sortRev[viewHoldings]
	sortRows(m.allCards, rev, cardCompare(key))
}

// sortArbRows orders the market rows: the section grouping always wins,
// and within each section that table's own column and direction apply.
func (m *Model) sortArbRows() {
	slices.SortStableFunc(m.marketRows, func(a, b market.Row) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		k := a.Kind
		c := marketKeyFor(marketSortColumns[k][m.marketSortIdx[k]], a, b)
		if m.marketSortRev[k] {
			c = -c
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
}

// sortRows stable-sorts in place, reversing the comparison when rev is set.
func sortRows[T any](rows []T, rev bool, compare func(a, b T) int) {
	slices.SortStableFunc(rows, func(a, b T) int {
		if c := compare(a, b); c != 0 {
			if rev {
				return -c
			}
			return c
		}
		return 0
	})
}

// Comparators return each column's natural direction: text ascending, money
// and counts descending — a pane sorted by price should lead with the money.
// Every one breaks ties by name so a hoard of $0.00 rows lists predictably.

func cardCompare(key string) func(a, b card) int {
	return func(a, b card) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "set":
			// Grouped by set alone, most valuable first within it — the
			// question this order answers is "what is my best card per set",
			// not "what is the collector-number sequence".
			c = strings.Compare(a.SetCode, b.SetCode)
			if c == 0 {
				c = cmp.Compare(b.Value, a.Value)
			}
		case "finish":
			c = strings.Compare(a.Finish, b.Finish)
		case "qty":
			c = cmp.Compare(b.Quantity, a.Quantity)
		case "price":
			c = cmp.Compare(priceOrder(b.Price), priceOrder(a.Price))
		default: // value
			c = cmp.Compare(b.Value, a.Value)
		}
		if c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Finish, b.Finish)
	}
}

// watchCompare orders the watches view. The default "state" leads with the
// watches that have met their threshold — the rows the view exists to
// surface — then unpriced last, then name.
func watchCompare(key string) func(a, b store.WatchStatus) int {
	rank := func(w store.WatchStatus) int {
		switch {
		case w.Met():
			return 0
		case w.PriceUSD != nil:
			return 1
		}
		return 2 // unpriced
	}
	return func(a, b store.WatchStatus) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "watch":
			if c = strings.Compare(a.Op, b.Op); c == 0 {
				c = cmp.Compare(b.Threshold, a.Threshold)
			}
		case "price":
			c = cmp.Compare(priceOrder(b.PriceUSD), priceOrder(a.PriceUSD))
		default: // state
			c = cmp.Compare(rank(a), rank(b))
		}
		if c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Finish, b.Finish)
	}
}

func moverCompare(key string) func(a, b store.PriceChange) int {
	return func(a, b store.PriceChange) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "set/num":
			c = comparePrinting(a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber)
		case "was":
			c = cmp.Compare(b.Old, a.Old)
		case "now":
			c = cmp.Compare(b.New, a.New)
		case "change":
			c = cmp.Compare(b.Pct(), a.Pct())
		case "qty":
			c = cmp.Compare(b.Copies, a.Copies)
		default: // impact, the magnitude ranking MoversByImpact established
			c = cmp.Compare(math.Abs(b.TotalDelta()), math.Abs(a.TotalDelta()))
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	}
}

func unpricedCompare(key string) func(a, b store.UnpricedRow) int {
	return func(a, b store.UnpricedRow) int {
		var c int
		switch key {
		case "set/num":
			c = comparePrinting(a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber)
		case "finish":
			c = strings.Compare(a.Finish, b.Finish)
		case "qty":
			c = cmp.Compare(b.Copies, a.Copies)
		case "held in":
			c = strings.Compare(a.HeldIn, b.HeldIn)
		default: // name, the store's own order
			c = strings.Compare(a.Name, b.Name)
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	}
}

// marketKeyFor compares one market-table column. Money and metric columns
// lead with the larger number; vendors and names read ascending.
func marketKeyFor(key string, a, b market.Row) int {
	switch key {
	case "name":
		return strings.Compare(a.Card.Name, b.Card.Name)
	case "set/num":
		return comparePrinting(a.Card.SetCode, a.Card.CollectorNumber,
			b.Card.SetCode, b.Card.CollectorNumber)
	case "last sold":
		return cmp.Compare(b.Market, a.Market)
	case "buylist":
		return cmp.Compare(b.SellAt, a.SellAt)
	case "to":
		return strings.Compare(a.SellTo, b.SellTo)
	case "ask":
		return cmp.Compare(b.BuyAt, a.BuyAt)
	case "at":
		return strings.Compare(a.BuyFrom, b.BuyFrom)
	case "pays":
		return cmp.Compare(b.Liquidity(), a.Liquidity())
	case "below":
		return cmp.Compare(b.BelowMarket(), a.BelowMarket())
	}
	// profit, each table's default metric
	return cmp.Compare(b.Profit(), a.Profit())
}

// comparePrinting orders by set, then collector number — numerically when both
// numbers are plain integers, so 2 comes before 10; suffixed promo numbers
// fall back to text.
func comparePrinting(aSet, aNum, bSet, bNum string) int {
	if c := strings.Compare(aSet, bSet); c != 0 {
		return c
	}
	na, errA := strconv.Atoi(aNum)
	nb, errB := strconv.Atoi(bNum)
	if errA == nil && errB == nil {
		return cmp.Compare(na, nb)
	}
	return strings.Compare(aNum, bNum)
}

// priceOrder reads a nullable price for ordering, with nil below every real price
// so unpriced rows sink in the natural money-first direction.
func priceOrder(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
