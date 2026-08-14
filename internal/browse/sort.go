package browse

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

var sortColumns = [...][]string{
	viewHoldings: {"value", "name", "set", "finish", "qty", "price"},
	viewMovers:   {"impact", "name", "set/num", "was", "now", "change", "qty"},
	viewWatches:  {"per-table"},
	viewMarket:   {"per-table"},
}

var marketSortColumns = [...][]string{
	market.KindProfit:      {"profit", "name", "set/num", "last sold", "buylist", "to"},
	market.KindLiquid:      {"pays", "name", "set/num", "last sold", "buylist", "to"},
	market.KindBelowMarket: {"below", "name", "set/num", "ask", "at", "last sold"},
}

func (m Model) selectedMarketKind() market.Kind {
	if sec, _ := m.marketCursorPos(); sec != compsSection {
		return market.Kind(sec)
	}
	return market.KindProfit
}

func (m Model) firstMarketRowOfKind(k market.Kind) int {
	return m.marketSections()[k].curStart
}

func (m Model) watchSortKey(s watchSection) string {
	cols := s.sortColumns()
	return cols[min(max(m.watchSortIdx[s], 0), len(cols)-1)]
}

func (m Model) sortLabel() string {
	if m.view == viewWatches {
		sec, _ := m.watchCursorPos()
		label := sec.title() + " · " + m.watchSortKey(sec)
		if m.watchSortRev[sec] {
			label += " (reversed)"
		}
		return label
	}
	if m.view == viewMarket {

		if m.selectedComp() != nil {
			label := "comps · " + m.compsSortKey()
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

func (m *Model) cycleSort() {
	if m.view == viewWatches {
		sec, _ := m.watchCursorPos()
		m.watchSortIdx[sec] = (m.watchSortIdx[sec] + 1) % len(sec.sortColumns())
		m.watchSortRev[sec] = false
		m.applySort()

		m.cursor[paneCards] = m.watchRegions()[sec].curStart
		m.watchSecOffset[sec] = 0
		m.scrollIntoView()
		return
	}
	if m.view == viewMarket {
		if m.selectedComp() != nil {
			m.compsSortIdx = (m.compsSortIdx + 1) % len(m.compsSortColumnsNow())
			m.compsSortRev = false

			m.marketPage[compsSection] = 0
			m.sortCompRows()
			m.cursor[paneCards] = m.marketSections()[compsSection].curStart
			m.scrollIntoView()
			return
		}
		k := m.selectedMarketKind()
		m.marketSortIdx[k] = (m.marketSortIdx[k] + 1) % len(marketSortColumns[k])
		m.marketSortRev[k] = false
		m.marketPage[k] = 0
		m.sortArbRows()
		m.cursor[paneCards] = m.firstMarketRowOfKind(k)
		m.scrollIntoView()
		return
	}
	v := m.view
	m.sortIdx[v] = (m.sortIdx[v] + 1) % len(sortColumns[v])
	m.sortRev[v] = false
	m.applySort()
}

func (m *Model) reverseSort() {
	if m.view == viewWatches {
		sec, _ := m.watchCursorPos()
		m.watchSortRev[sec] = !m.watchSortRev[sec]
		m.applySort()
		m.cursor[paneCards] = m.watchRegions()[sec].curStart
		m.watchSecOffset[sec] = 0
		m.scrollIntoView()
		return
	}
	if m.view == viewMarket {
		if m.selectedComp() != nil {
			m.compsSortRev = !m.compsSortRev
			m.marketPage[compsSection] = 0
			m.sortCompRows()
			m.cursor[paneCards] = m.marketSections()[compsSection].curStart
			m.scrollIntoView()
			return
		}
		k := m.selectedMarketKind()
		m.marketSortRev[k] = !m.marketSortRev[k]
		m.marketPage[k] = 0
		m.sortArbRows()
		m.cursor[paneCards] = m.firstMarketRowOfKind(k)
		m.scrollIntoView()
		return
	}
	m.sortRev[m.view] = !m.sortRev[m.view]
	m.applySort()
}

func (m *Model) applySort() {
	key, rev := sortColumns[m.view][m.sortIdx[m.view]], m.sortRev[m.view]
	switch m.view {
	case viewWatches:

		sortRows(m.overs, m.watchSortRev[secOvers], watchCompare(m.watchSortKey(secOvers)))
		sortRows(m.unders, m.watchSortRev[secUnders], watchCompare(m.watchSortKey(secUnders)))
		sortRows(m.unpriced, m.watchSortRev[secUnpriced], unpricedCompare(m.watchSortKey(secUnpriced)))
	case viewMovers:

		sortRows(m.filteredMovers, rev, moverCompare(key))
		m.deriveMoversPage()
	case viewMarket:
		m.sortArbRows()
	default:
		m.sortHoldings()
		m.applyFilter()
	}
}

func (m *Model) sortHoldings() {
	key, rev := sortColumns[viewHoldings][m.sortIdx[viewHoldings]], m.sortRev[viewHoldings]
	sortRows(m.allCards, rev, cardCompare(key))
}

func (m *Model) sortArbRows() {
	slices.SortStableFunc(m.marketAllRows, func(a, b market.Row) int {
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
	m.deriveMarketPages()
}

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

func cardCompare(key string) func(a, b card) int {
	return func(a, b card) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "set":

			c = strings.Compare(a.SetCode, b.SetCode)
			if c == 0 {
				c = cmp.Compare(b.Value, a.Value)
			}
		case "finish":
			c = strings.Compare(a.Finish.String(), b.Finish.String())
		case "qty":
			c = cmp.Compare(b.Quantity, a.Quantity)
		case "price":
			c = cmp.Compare(priceOrder(b.Price), priceOrder(a.Price))
		default:
			c = cmp.Compare(b.Value, a.Value)
		}
		if c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Finish.String(), b.Finish.String())
	}
}

func watchCompare(key string) func(a, b store.WatchStatus) int {
	rank := func(w store.WatchStatus) int {
		switch {
		case w.Met():
			return 0
		case w.PriceUSD != nil:
			return 1
		}
		return 2
	}
	return func(a, b store.WatchStatus) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "threshold":

			c = cmp.Compare(b.Threshold, a.Threshold)
		case "price":
			c = cmp.Compare(priceOrder(b.PriceUSD), priceOrder(a.PriceUSD))
		default:
			c = cmp.Compare(rank(a), rank(b))
		}
		if c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Finish.String(), b.Finish.String())
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
		default:

			c = cmp.Compare(b.TotalDelta(), a.TotalDelta())
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
			c = strings.Compare(a.Finish.String(), b.Finish.String())
		case "qty":
			c = cmp.Compare(b.Copies, a.Copies)
		case "held in":
			c = strings.Compare(a.HeldIn, b.HeldIn)
		default:
			c = strings.Compare(a.Name, b.Name)
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	}
}

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

	return cmp.Compare(b.Profit(), a.Profit())
}

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

func priceOrder(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
