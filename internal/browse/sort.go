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

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/store"
)

// sortColumns is each view's sortable columns, in cycle order, named as their
// headers read. Index 0 is the default and reproduces the order rows already
// arrive in, so an untouched pane looks exactly as it always has.
//
// Arbitrage has no "why" entry because the kind grouping is not a sort: it is
// the view's reading order, and every column sorts within it (gain, the
// default, is the ranking the sections already use).
var sortColumns = [...][]string{
	viewHoldings:  {"value", "name", "set/num", "finish", "qty", "price"},
	viewMovers:    {"impact", "name", "set/num", "finish", "was", "now", "change", "qty"},
	viewUnpriced:  {"name", "set/num", "finish", "qty", "held in"},
	viewWatches:   {"state", "name", "watch", "price"},
	viewArbitrage: {"gain", "name", "set/num", "buy", "from", "sell", "to"},
}

// sortLabel is how the status line describes the focused view's order.
func (m Model) sortLabel() string {
	label := sortColumns[m.view][m.sortIdx[m.view]]
	if m.sortRev[m.view] {
		label += " (reversed)"
	}
	return label
}

// cycleSort moves the current view to its next column, forward order.
func (m *Model) cycleSort() {
	v := m.view
	m.sortIdx[v] = (m.sortIdx[v] + 1) % len(sortColumns[v])
	m.sortRev[v] = false
	m.applySort()
}

// reverseSort flips the current column's direction.
func (m *Model) reverseSort() {
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
	case viewArbitrage:
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

// sortArbRows orders the arbitrage rows by that view's own state, kept
// separate from applySort because rows can arrive after the user has moved on.
func (m *Model) sortArbRows() {
	key, rev := sortColumns[viewArbitrage][m.sortIdx[viewArbitrage]], m.sortRev[viewArbitrage]
	slices.SortStableFunc(m.arbRows, func(a, b arbitrage.Row) int {
		// The kind grouping survives both the column choice and the reversal:
		// reversing GAIN should not put spreads above real arbitrage.
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		c := arbKey(key, a, b)
		if rev {
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
		case "set/num":
			c = comparePrinting(a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber)
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
		case "finish":
			c = strings.Compare(a.Finish, b.Finish)
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

// arbKey compares one arbitrage column. The SELL and TO columns compare what
// the row displays: a spread row shows the dearest retail there, not a buylist.
func arbKey(key string, a, b arbitrage.Row) int {
	switch key {
	case "name":
		return strings.Compare(a.Card.Name, b.Card.Name)
	case "set/num":
		return comparePrinting(a.Card.SetCode, a.Card.CollectorNumber,
			b.Card.SetCode, b.Card.CollectorNumber)
	case "buy":
		return cmp.Compare(b.BuyAt, a.BuyAt)
	case "from":
		return strings.Compare(a.BuyFrom, b.BuyFrom)
	case "sell":
		return cmp.Compare(arbSell(b), arbSell(a))
	case "to":
		return strings.Compare(arbTo(a), arbTo(b))
	default: // gain, the ranking the sections arrived in
		return cmp.Compare(arbGain(b), arbGain(a))
	}
}

func arbSell(r arbitrage.Row) float64 {
	if r.Kind == arbitrage.KindSpread {
		return r.DearAt
	}
	return r.SellAt
}

func arbTo(r arbitrage.Row) string {
	if r.Kind == arbitrage.KindSpread {
		return r.DearFrom
	}
	return r.SellTo
}

// arbGain is the row's own metric, so within a kind the units agree.
func arbGain(r arbitrage.Row) float64 {
	switch r.Kind {
	case arbitrage.KindProfit:
		return r.Profit()
	case arbitrage.KindLiquid:
		return r.Liquidity()
	}
	return r.Spread()
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
