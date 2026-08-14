package browse

import (
	"cmp"
	"math"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/ui"
)

func (m Model) marketTotalRows() int {
	return len(m.marketRows) + len(m.marketComps)
}

func (m Model) marketCursorSlots() int {
	var n int
	for _, s := range m.marketSections() {
		n += s.span
	}
	return n
}

func (m Model) selectedComp() *market.Comp {
	sec, idx := m.marketCursorPos()
	if sec != compsSection || idx >= len(m.marketComps) {
		return nil
	}
	return &m.marketComps[idx]
}

type marketSection struct {
	rowStart, count int
	curStart, span  int
}

const compsSection = 2

func (m Model) marketSections() [3]marketSection {
	var s [3]marketSection
	for _, r := range m.marketRows {

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

func (m Model) firstMarketCursor() int {
	for _, s := range m.marketSections() {
		if s.count > 0 {
			return s.curStart
		}
	}
	return 0
}

func (m Model) selectedMarketRow() *market.Row {
	sec, idx := m.marketCursorPos()
	secs := m.marketSections()
	if sec == compsSection || secs[sec].count == 0 {
		return nil
	}
	return &m.marketRows[secs[sec].rowStart+idx]
}

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

var (
	compsSellSortColumns = []string{"price dispersion", "name", "set/num", "fin", "tcg sold", "mp", "ck"}
	compsBuySortColumns  = []string{"spread", "name", "set/num", "fin", "tcg sold", "mp", "ck", "ck buylist"}
)

func (m Model) compsSortColumnsNow() []string {
	if m.compsBuySide {
		return compsBuySortColumns
	}
	return compsSellSortColumns
}

func (m Model) compsSortKey() string {
	cols := m.compsSortColumnsNow()
	return cols[min(max(m.compsSortIdx, 0), len(cols)-1)]
}

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
		return strings.Compare(a.Card.Finish.String(), b.Card.Finish.String())
	}

	if buySide {
		return cmp.Compare(spreadOrInf(a), spreadOrInf(b))
	}
	return cmp.Compare(saleSpreadOrInf(a), saleSpreadOrInf(b))
}

func spreadOrInf(c market.Comp) float64 {
	if !c.HasSpread() {
		return math.Inf(1)
	}
	return c.Spread()
}

func saleSpreadOrInf(c market.Comp) float64 {
	s, ok := c.SaleSpread()
	if !ok {
		return math.Inf(1)
	}
	return s
}

const (
	compsSellNote = "vendor sale prices"
	compsBuyNote  = "the cash bid against the asks"
)

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

func compSaleSpreadCell(env ui.Env, c market.Comp) ui.Cell {
	s, ok := c.SaleSpread()
	if !ok {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.SaleSpreadGrade(s))}
}

func compMoney(has bool, v float64) string {
	if !has {
		return "—"
	}
	return ui.Money(v)
}

func compSpreadCell(env ui.Env, c market.Comp) ui.Cell {
	if !c.HasSpread() {
		return ui.Cell{Text: "—", Style: env.Dim()}
	}
	s := c.Spread()
	return ui.Cell{Text: ui.PercentAlways(s), Style: env.Heat(market.MarkupGrade(s))}
}
