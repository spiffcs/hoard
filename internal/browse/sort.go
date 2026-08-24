package browse

import (
	"cmp"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

var sortColumns = [...][]string{
	viewHoldings: {"value", "name", "set", "finish", "qty", "price"},
	viewMovers:   {"impact", "name", "set/num", "was", "now", "change", "qty"},
	viewWatches:  {"per-table"},
	viewMarket:   {"per-table"},
	viewDip:      {"per-table"},
}

var unownedSortColumns = []string{"value", "name", "set", "price"}

func (m Model) holdingsSortColumns() []string {
	if m.setUnowned {
		return unownedSortColumns
	}
	return sortColumns[viewHoldings]
}

func (m Model) viewSortColumns() []string {
	if m.view == viewHoldings {
		return m.holdingsSortColumns()
	}
	return sortColumns[m.view]
}

func (m *Model) keepSortKey(was []string) {
	key := ""
	if i := m.sortIdx[viewHoldings]; i >= 0 && i < len(was) {
		key = was[i]
	}
	if i := slices.Index(m.holdingsSortColumns(), key); i >= 0 {
		m.sortIdx[viewHoldings] = i
		return
	}
	m.sortIdx[viewHoldings], m.sortRev[viewHoldings] = 0, false
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

func (m Model) dipSortKey(s dipSection) string {
	cols := s.sortColumns()
	return cols[min(max(m.dipSortIdx[s], 0), len(cols)-1)]
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
	if m.view == viewDip {
		sec, _ := m.dipCursorPos()
		label := dipSection(sec).title() + " · " + m.dipSortKey(dipSection(sec))
		if m.dipSortRev[sec] {
			label += " (reversed)"
		}
		return label
	}
	label := m.viewSortColumns()[m.sortIdx[m.view]]
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
	if m.view == viewDip {
		sec, _ := m.dipCursorPos()
		m.dipSortIdx[sec] = (m.dipSortIdx[sec] + 1) % len(dipSection(sec).sortColumns())
		m.dipSortRev[sec] = false
		m.applySort()
		m.cursor[paneCards] = m.dipSectionsInfo()[sec].curStart
		m.dipSecOffset[sec] = 0
		m.scrollIntoView()
		return
	}
	v := m.view
	m.sortIdx[v] = (m.sortIdx[v] + 1) % len(m.viewSortColumns())
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
	if m.view == viewDip {
		sec, _ := m.dipCursorPos()
		m.dipSortRev[sec] = !m.dipSortRev[sec]
		m.applySort()
		m.cursor[paneCards] = m.dipSectionsInfo()[sec].curStart
		m.dipSecOffset[sec] = 0
		m.scrollIntoView()
		return
	}
	m.sortRev[m.view] = !m.sortRev[m.view]
	m.applySort()
}

func (m *Model) applySort() {
	key, rev := m.viewSortColumns()[m.sortIdx[m.view]], m.sortRev[m.view]
	switch m.view {
	case viewWatches:

		sortRows(m.overs, m.watchSortRev[secOvers], watchCompare(m.watchSortKey(secOvers)), watchTiebreak)
		sortRows(m.unders, m.watchSortRev[secUnders], watchCompare(m.watchSortKey(secUnders)), watchTiebreak)
		sortRows(m.unpriced, m.watchSortRev[secUnpriced], unpricedCompare(m.watchSortKey(secUnpriced)), unpricedTiebreak)
	case viewMovers:

		sortRows(m.filteredMovers, rev, moverCompare(key), moverTiebreak)
		m.deriveMoversPage()
	case viewDip:
		sortRows(m.filteredDips, m.dipSortRev[secDip], trendCompare(m.dipSortKey(secDip)), trendTiebreak)
		sortRows(m.filteredMomentum, m.dipSortRev[secMomentum],
			trendCompare(m.dipSortKey(secMomentum)), trendTiebreak)
		m.deriveDipPages()
	case viewMarket:
		m.sortArbRows()
	default:
		m.sortHoldings()
		m.applyFilter()
	}
}

func (m *Model) sortHoldings() {
	key, rev := m.holdingsSortColumns()[m.sortIdx[viewHoldings]], m.sortRev[viewHoldings]
	sortRows(m.allCards, rev, cardCompare(key), cardTiebreak)
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
		return breakTie(a.Card.Name, b.Card.Name,
			a.Card.SetCode, a.Card.CollectorNumber, b.Card.SetCode, b.Card.CollectorNumber,
			a.Card.Finish.String(), b.Card.Finish.String())
	})
	m.deriveMarketPages()
}

func sortRows[T any](rows []T, rev bool, compare, tiebreak func(a, b T) int) {
	slices.SortStableFunc(rows, func(a, b T) int {
		if c := compare(a, b); c != 0 {
			if rev {
				return -c
			}
			return c
		}

		return tiebreak(a, b)
	})
}

func breakTie(aName, bName, aSet, aNum, bSet, bNum, aFin, bFin string) int {
	if c := strings.Compare(aName, bName); c != 0 {
		return c
	}
	if c := comparePrinting(aSet, aNum, bSet, bNum); c != 0 {
		return c
	}
	return strings.Compare(aFin, bFin)
}

func cardTiebreak(a, b card) int {
	return breakTie(a.Name, b.Name, a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber,
		a.Finish.String(), b.Finish.String())
}

func watchTiebreak(a, b store.WatchStatus) int {
	return breakTie(a.Name, b.Name, a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber,
		a.Finish.String(), b.Finish.String())
}

func moverTiebreak(a, b store.PriceChange) int {
	return breakTie(a.Name, b.Name, a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber,
		a.Finish.String(), b.Finish.String())
}

func unpricedTiebreak(a, b store.UnpricedRow) int {
	return breakTie(a.Name, b.Name, a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber,
		a.Finish.String(), b.Finish.String())
}

func trendTiebreak(a, b store.TrendRow) int {
	return breakTie(a.Name, b.Name, a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber,
		a.Finish.String(), b.Finish.String())
}

func cardCompare(key string) func(a, b card) int {
	return func(a, b card) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "set":

			c = comparePrinting(a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber)
		case "finish":
			c = strings.Compare(a.Finish.String(), b.Finish.String())
		case "qty":
			c = cmp.Compare(b.Quantity, a.Quantity)
		case "price":
			c = cmp.Compare(priceOrder(b.Price), priceOrder(a.Price))
		default:
			c = cmp.Compare(b.Value, a.Value)
		}
		return c
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
		return c
	}
}

func trendCompare(key string) func(a, b store.TrendRow) int {
	return func(a, b store.TrendRow) int {
		var c int
		switch key {
		case "name":
			c = strings.Compare(a.Name, b.Name)
		case "set/num":
			c = comparePrinting(a.SetCode, a.CollectorNumber, b.SetCode, b.CollectorNumber)
		case "finish":
			c = strings.Compare(a.Finish.String(), b.Finish.String())
		case "high":
			c = cmp.Compare(b.High, a.High)
		case "low":
			c = cmp.Compare(b.Low, a.Low)
		case "was":
			c = cmp.Compare(b.First, a.First)
		case "now":
			c = cmp.Compare(b.Last, a.Last)
		case "streak":
			c = cmp.Compare(b.Ups, a.Ups)
			if c == 0 {
				c = cmp.Compare(b.Change(), a.Change())
			}
		case "change":
			c = cmp.Compare(b.Change(), a.Change())
		default:

			c = cmp.Compare(a.OffHigh(), b.OffHigh())
		}
		return c
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
		return c
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
		return c
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
	return compareNatural(aNum, bNum)
}

func compareNatural(a, b string) int {
	for a != "" && b != "" {
		digitsA, digitsB := isDigit(a[0]), isDigit(b[0])
		if digitsA != digitsB {
			if digitsA {
				return -1
			}
			return 1
		}
		var runA, runB string
		if digitsA {
			runA, a = splitRun(a, true)
			runB, b = splitRun(b, true)
			if c := compareNumeric(runA, runB); c != 0 {
				return c
			}
			continue
		}
		runA, a = splitRun(a, false)
		runB, b = splitRun(b, false)
		if c := strings.Compare(runA, runB); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func splitRun(s string, digits bool) (run, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) == digits {
		i++
	}
	return s[:i], s[i:]
}

func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return strings.Compare(a, b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func priceOrder(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
