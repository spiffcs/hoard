package browse

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type MarketFunc func(ctx context.Context, p progress.Fn, min float64) (market.Result, error)

const defaultMarketFloor = 1.00

const singleTablePageSize = 59

const pageSize = 50

type marketMsg struct {
	gen int
	res market.Result
	err error
}

func (m *Model) startMarketFetch() tea.Cmd {
	if m.marketFetch == nil {
		m.status, m.statusErr = "vendor quotes are unavailable in this build", true
		return nil
	}
	if m.marketLoading {

		m.status, m.statusErr = "already fetching vendor prices · esc cancels", false
		return nil
	}
	if m.marketLoaded {

		m.marketLoaded = false
	}

	m.cancelMarketFetch()
	ctx, cancel := context.WithCancel(m.ctx)
	m.marketCancel = cancel
	m.marketLoading = true

	m.status, m.statusErr = "", false
	m.marketGen++
	gen, fetch := m.marketGen, m.marketFetch

	min := m.activeMarketFloor()
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		res, err := fetch(ctx, nil, min)
		return marketMsg{gen: gen, res: res, err: err}
	})
}

func (m Model) onMarket(msg marketMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.marketGen {
		return m, nil
	}
	m.marketLoading = false
	m.cancelMarketFetch()

	if msg.err != nil {

		if m.ctx.Err() == nil && !isCanceled(msg.err) {
			m.setError(msg.err)
		}
		return m, nil
	}
	m.marketResult = msg.res
	m.applyMarketRows()
	m.marketLoaded = true
	m.status = ""
	m.clampCursor(paneCards)
	return m, nil
}

func (m *Model) cancelMarketFetch() {
	if m.marketCancel != nil {
		m.marketCancel()
		m.marketCancel = nil
	}
}

func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

type MarketCachedFunc func(min float64) (market.Result, bool)

func (m *Model) loadCachedMarket() {
	if m.marketCached == nil || m.marketLoaded || m.marketLoading {
		return
	}
	res, ok := m.marketCached(m.activeMarketFloor())
	if !ok {
		return
	}
	m.marketResult = res
	m.applyMarketRows()
	m.marketLoaded = true
	m.status, m.statusErr = "vendor quotes from earlier today · F refetches", false

	if !m.marketPennies {
		m.status += " · penny filter < " + ui.Money(m.marketFloor)
	}
}

func (m Model) activeMarketFloor() float64 {
	if m.marketPennies {
		return 0
	}
	return m.marketFloor
}

func (m *Model) refreshMarketFloor() {
	if !m.marketLoaded || m.marketCached == nil {
		return
	}
	res, ok := m.marketCached(m.activeMarketFloor())
	if !ok {
		m.marketLoaded = false
		m.status, m.statusErr = "quotes expired · press F to fetch today's", false
		return
	}
	m.marketResult = res
	m.applyMarketRows()
	m.clampCursor(paneCards)
}

func (m Model) marketLines(width int) []string {
	_, filtered := m.filterContainerID()
	if width <= 0 {
		return nil
	}
	if m.marketTotalRows() == 0 && (!m.marketLoaded || (!filtered && m.filter.empty())) {

		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	secs := m.marketSections()
	budgets := m.marketSectionBudgets()
	totals := m.marketSectionTotals()

	cursorSec, cursorIdx := m.marketCursorPos()
	hasCursor := m.focus == paneCards
	emptyNote := m.marketEmptyNote(filtered)

	var out []string
	for i, sec := range secs {
		if i > 0 {
			out = append(out, "")
		}
		title, note := m.marketSectionHead(i)
		head := m.theme.Title.Render(title) + "  " + m.theme.Help.Render(note)
		if frag := pagePhrase(m.marketSecOffset[i], budgets[i], sec.count,
			m.marketPage[i], totals[i], pageSize); frag != "" {
			head += m.theme.Help.Render(frag)
		}
		if sec.count == 0 {

			if hasCursor && i == cursorSec {
				head = ui.Restyle(fit(head, width), m.theme.Cursor)
			}
			out = append(out, head, m.theme.Help.Render(emptyNote))
			continue
		}
		out = append(out, head)

		var t ui.Table
		if i == compsSection {
			t = compsSectionTable(env, m.marketComps, m.compsBuySide)
		} else {
			t = marketSectionTable(env, market.Kind(i),
				m.marketRows[sec.rowStart:sec.rowStart+sec.count], m.liquidLowball)
		}
		t.Env, t.Header = env, true
		lines := t.Lines()
		out = append(out, lines[0])
		off := m.marketSecOffset[i]
		for r := off; r < min(off+budgets[i], sec.count); r++ {
			line := lines[1+r]
			if hasCursor && i == cursorSec && r == cursorIdx {

				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

func (m Model) marketEmptyNote(filtered bool) string {

	if note := m.filterUnsupported(); note != "" {
		return note
	}
	if !m.filter.empty() {
		return "none match " + m.filter.raw
	}
	if filtered {
		return "none in this collection"
	}
	return "nothing today"
}

func (m Model) marketSectionHead(i int) (title, note string) {
	if i == compsSection {
		if m.compsBuySide {
			return market.CompsTitle + " · BUY", compsBuyNote
		}
		return market.CompsTitle + " · SELL", compsSellNote
	}
	kind := market.Kind(i)

	if kind == market.KindLiquid && m.liquidLowball {
		kind = market.KindLowball
	}
	return kind.Title(), kind.Note()
}

func marketSectionTable(env ui.Env, kind market.Kind, rows []market.Row, lowball bool) ui.Table {
	name := ui.Col{Title: "NAME", Align: ui.Left, Flex: true, Min: 10}
	setNum := ui.Col{Title: "SET/NUM", Align: ui.Left, Priority: 6, Style: env.Dim()}
	fin := ui.Col{Title: "FIN", Align: ui.Left, Priority: 5, Style: env.Dim()}
	vendor := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Left, Priority: 4, Style: env.Dim()} }
	money := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Right} }

	cardCells := func(r market.Row) []ui.Cell {
		return []ui.Cell{
			{Text: r.Card.Name, Style: env.Identity(r.Card.ColorIdentity)},
			ui.C(r.Printing()), ui.C(ui.FinishTreated(r.Card.Finish, r.Card.Treatment)),
		}
	}

	var t ui.Table
	switch kind {
	case market.KindProfit:

		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("TCG SOLD"), money("CK BUYLIST"), money("PROFIT")}}
		for _, r := range rows {

			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)),
				ui.Cell{Text: "+" + ui.Money(r.Profit()), Style: env.Gain()})...)
		}
	case market.KindLiquid:

		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("TCG SOLD"), money("CK BUYLIST"), money("PAYS")}}
		for _, r := range rows {

			pays := ui.Cell{Text: ui.Percent(r.Liquidity()),
				Style: env.Grade(market.LiquidityGrade(r.Liquidity()))}
			if lowball {
				pays.Style = env.Heat(market.LowballGrade(r.Liquidity()))
			}
			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)), pays)...)
		}
	default:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("ASK"), vendor("AT"), money("TCG SOLD"), money("BELOW")}}
		for _, r := range rows {
			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.BuyAt)), ui.C(r.BuyFrom),
				ui.C(ui.Money(r.Market)),
				ui.Cell{Text: "-" + ui.Percent(r.BelowMarket()),
					Style: env.Grade(market.BelowMarketGrade(r.BelowMarket()))})...)
		}
	}
	return t
}

func (m Model) marketHeader() (title, totals string) {
	name := "MARKET" + m.viewScope()
	switch {
	case m.marketLoading:
		return name, "asking vendors…"
	case !m.marketLoaded:
		return name, "press F to fetch"
	}
	return name, fmt.Sprintf("%s rows · %s printings compared",
		ui.Count(m.marketTotalRows()), ui.Count(m.marketResult.Compared))
}

func (m Model) marketStatus() string {
	switch {
	case m.marketLoading:
		return m.spinner.View() + " reading today's vendor prices (first read of the day downloads ~5 MB)"
	case !m.marketLoaded:
		return m.theme.Help.Render(
			"vendor quotes may need a download, so the view waits to be asked · press F")
	case m.marketTotalRows() == 0:

		if note := m.filterUnsupported(); note != "" {
			return m.theme.Err.Render(note)
		}
		if !m.filter.empty() {
			return m.theme.Help.Render(fmt.Sprintf(
				"nothing matches %s · esc clears the filter", m.filter.raw))
		}
		if sel := m.selectedContainer(); sel != nil && sel.Kind != kindAllCards {
			return m.theme.Help.Render(fmt.Sprintf(
				"no market rows in %s · All Cards shows every container", sel.Name))
		}
		return m.theme.Help.Render("no vendor disagreed about anything you own today")
	}

	secs := m.marketSections()
	sec, idx := m.marketCursorPos()

	suffix := "one-day vendor prices"
	if m.selectedComp() != nil {
		suffix = "PRICE DISPERSION = high sale minus low sale, over high"
		if m.compsBuySide {
			suffix = "SPREAD = 1 − BUYLIST ÷ LOW"
		}
	} else if m.liquidLowball && sec == int(market.KindLiquid) {

		suffix = "lowball band · 'b' for the shops paying near market"
	}

	title, _ := m.marketSectionHead(sec)
	line := fmt.Sprintf("%s · empty", title)
	if secs[sec].count > 0 {
		line = fmt.Sprintf("%d/%d · %s", idx+1, secs[sec].count, m.selectedMarketNote())
	}

	if name := m.selectedItemName(); name != "" {
		line = name + " · " + line
	}

	if !m.filter.empty() {
		line += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	return m.theme.Help.Render(line + " · " + suffix)
}

func (m Model) selectedMarketNote() string {

	if c := m.selectedComp(); c != nil {
		if m.compsBuySide {

			if !c.HasBuylist {
				return "no buylist bid today"
			}
			note := c.BuylistTo + " pays " + ui.Money(c.Buylist)
			if c.HasSpread() {
				note += " · spread " + ui.PercentAlways(c.Spread())
			}
			return note
		}

		var parts []string
		if c.HasMarket {
			parts = append(parts, "last sold "+ui.Money(c.Market))
		}
		if c.HasManapool {
			parts = append(parts, "mp asks "+ui.Money(c.Manapool))
		}
		if c.HasCK {
			parts = append(parts, "ck asks "+ui.Money(c.CK))
		}
		return strings.Join(parts, " · ")
	}

	row := m.selectedMarketRow()
	if row == nil {
		return ""
	}
	r := *row
	switch r.Kind {
	case market.KindProfit:
		return fmt.Sprintf("%s pays %s · tcg last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	case market.KindLiquid:

		if m.liquidLowball {
			return fmt.Sprintf("%s pays only %s · tcg last sold for %s",
				r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
		}
		return fmt.Sprintf("%s pays %s · tcg last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	}
	return fmt.Sprintf("%s asks %s · tcg last sold for %s",
		r.BuyFrom, ui.Money(r.BuyAt), ui.Money(r.Market))
}

func (m *Model) applyMarketRows() {
	res := m.marketResult

	scopeOwned := func(card store.OwnedFinish, qty int) store.OwnedFinish {
		if card.Copies > 0 && qty != card.Copies {
			card.Value = card.Value / float64(card.Copies) * float64(qty)
		}
		card.Copies = qty
		return card
	}
	if cid, filtered := m.filterContainerID(); filtered {
		scoped := market.Result{Compared: res.Compared}
		for _, o := range res.Opportunities {
			if qty := m.containerQty(cid, o.Card.ScryfallID, o.Card.Finish); qty > 0 {
				o.Card = scopeOwned(o.Card, qty)
				scoped.Opportunities = append(scoped.Opportunities, o)
			}
		}
		for _, c := range res.Comps {
			if qty := m.containerQty(cid, c.Card.ScryfallID, c.Card.Finish); qty > 0 {
				c.Card = scopeOwned(c.Card, qty)
				scoped.Comps = append(scoped.Comps, c)
			}
		}
		res = scoped
	}

	if code, ok := m.filterSetCode(); ok {
		scoped := market.Result{Compared: res.Compared}
		for _, o := range res.Opportunities {
			if o.Card.SetCode == code {
				scoped.Opportunities = append(scoped.Opportunities, o)
			}
		}
		for _, c := range res.Comps {
			if c.Card.SetCode == code {
				scoped.Comps = append(scoped.Comps, c)
			}
		}
		res = scoped
	}

	if !m.filter.empty() {
		queried := market.Result{Compared: res.Compared}
		for _, o := range res.Opportunities {
			if m.filter.matches(marketAsCard(o.Card), m.allowed) {
				queried.Opportunities = append(queried.Opportunities, o)
			}
		}
		for _, c := range res.Comps {
			if m.filter.matches(marketAsCard(c.Card), m.allowed) {
				queried.Comps = append(queried.Comps, c)
			}
		}
		res = queried
	}

	rows := market.Rows(res, len(res.Opportunities))
	if m.liquidLowball {

		swapped := make([]market.Row, 0, len(rows))
		for _, r := range rows {
			if r.Kind != market.KindLiquid {
				swapped = append(swapped, r)
			}
		}
		for _, o := range market.Lowballs(res, len(res.Opportunities)) {
			swapped = append(swapped, market.Row{Opportunity: o, Kind: market.KindLiquid})
		}
		rows = swapped
	}
	kept := rows[:0]
	for _, r := range rows {

		if r.Kind != market.KindProfit && r.Kind != market.KindLiquid {
			continue
		}
		if min := m.floorMin(); min > 0 {
			unit := r.Card.Value
			if r.Card.Copies > 1 {
				unit /= float64(r.Card.Copies)
			}
			if unit < min {
				continue
			}
		}
		kept = append(kept, r)
	}
	m.marketAllRows = kept

	m.marketPage = [3]int{}
	m.sortArbRows()
	m.applyMarketComps(res.Comps)

	m.marketSecOffset = [3]int{}

	sec, _ := m.marketCursorPos()
	if m.marketSections()[sec].count == 0 {
		m.cursor[paneCards] = m.firstMarketCursor()
	}
}

func (m Model) marketSectionTotals() [3]int {
	var t [3]int
	for _, r := range m.marketAllRows {

		if r.Kind < 0 || int(r.Kind) >= compsSection {
			continue
		}
		t[r.Kind]++
	}
	t[compsSection] = len(m.marketAllComps)
	return t
}

func (m *Model) deriveMarketPages() {
	totals := m.marketSectionTotals()
	for i, tot := range totals {
		maxPage := 0
		if tot > 0 {
			maxPage = (tot - 1) / pageSize
		}
		m.marketPage[i] = min(max(m.marketPage[i], 0), maxPage)
	}
	page := func(pg, tot int) (lo, hi int) {
		lo = min(pg*pageSize, tot)
		return lo, min(lo+pageSize, tot)
	}
	rows := make([]market.Row, 0, min(len(m.marketAllRows), 2*pageSize))
	start := 0
	for k := 0; k < compsSection; k++ {
		run := m.marketAllRows[start : start+totals[k]]
		lo, hi := page(m.marketPage[k], totals[k])
		rows = append(rows, run[lo:hi]...)
		start += totals[k]
	}
	m.marketRows = rows
	lo, hi := page(m.marketPage[compsSection], totals[compsSection])
	m.marketComps = m.marketAllComps[lo:hi]
}

func pagePhrase(off, visible, count, page, tot, size int) string {
	frag := ""
	if count > visible && visible > 0 {
		frag = fmt.Sprintf(" · %d–%d of %d", off+1, min(off+visible, count), count)
	}
	if tot > size {
		lo := page*size + 1
		frag += fmt.Sprintf(" · %d–%d of %d", lo, min(lo+size-1, tot), tot)
	}
	return frag
}

func (m Model) tablePagePhrase(count, page, tot int) string {
	return pagePhrase(m.offset[paneCards], max(m.visibleRows()-1, 0), count, page, tot,
		singleTablePageSize)
}

func (m *Model) turnTablePage(dir int) {
	switch m.view {
	case viewMarket:
		m.turnMarketPage(dir)
	case viewMovers:
		m.turnOnePage(dir, &m.moversPage, len(m.filteredMovers), m.deriveMoversPage)
	default:
		m.turnOnePage(dir, &m.cardsPage, len(m.filteredCards), m.deriveCardsPage)
	}
}

func (m *Model) turnOnePage(dir int, page *int, tot int, derive func()) {
	maxPage := 0
	if tot > 0 {
		maxPage = (tot - 1) / singleTablePageSize
	}
	next := min(max(*page+dir, 0), maxPage)
	if next == *page {
		if maxPage == 0 {
			m.status, m.statusErr = "one page here", false
		} else if dir > 0 {
			m.status, m.statusErr = "last page", false
		} else {
			m.status, m.statusErr = "first page", false
		}
		return
	}
	*page = next
	derive()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0

	m.status, m.statusErr = fmt.Sprintf("page %d/%d · rows %d–%d of %d · sorted by %s",
		next+1, maxPage+1, next*singleTablePageSize+1,
		min((next+1)*singleTablePageSize, tot), tot, m.sortLabel()), false
}

func (m *Model) turnMarketPage(dir int) {
	sec, _ := m.marketCursorPos()
	tot := m.marketSectionTotals()[sec]
	maxPage := 0
	if tot > 0 {
		maxPage = (tot - 1) / pageSize
	}
	next := min(max(m.marketPage[sec]+dir, 0), maxPage)
	if next == m.marketPage[sec] {
		if maxPage == 0 {
			m.status, m.statusErr = "one page here", false
		} else if dir > 0 {
			m.status, m.statusErr = "last page", false
		} else {
			m.status, m.statusErr = "first page", false
		}
		return
	}
	m.marketPage[sec] = next
	m.deriveMarketPages()
	m.marketSecOffset[sec] = 0
	m.cursor[paneCards] = m.marketSections()[sec].curStart
	m.scrollIntoView()
	m.status, m.statusErr = fmt.Sprintf("page %d/%d · rows %d–%d of %d · sorted by %s",
		next+1, maxPage+1, next*pageSize+1, min((next+1)*pageSize, tot), tot,
		m.sortLabel()), false
}

func (m Model) marketSectionBudgets() [3]int {
	secs := m.marketSections()
	counts := make([]int, len(secs))
	for i, s := range secs {
		counts[i] = s.count
	}

	pool := max(m.visibleRows()-(2+3*2), 0)
	sec, _ := m.marketCursorPos()
	var budget [3]int
	copy(budget[:], sectionBudgets(counts, pool, sec))
	return budget
}

func (m *Model) jumpMarketSection(dir int) {
	secs := m.marketSections()
	cur, _ := m.marketCursorPos()
	for i := cur + dir; i >= 0 && i < len(secs); i += dir {

		m.cursor[paneCards] = secs[i].curStart

		m.focus = paneCards
		m.scrollIntoView()
		return
	}
}

func (m *Model) scrollMarketIntoView() {
	secs := m.marketSections()
	budgets := m.marketSectionBudgets()
	if m.marketTotalRows() > 0 {
		sec, idx := m.marketCursorPos()
		if b := budgets[sec]; b > 0 {
			if idx < m.marketSecOffset[sec] {
				m.marketSecOffset[sec] = idx
			}
			if idx >= m.marketSecOffset[sec]+b {
				m.marketSecOffset[sec] = idx - b + 1
			}
		}
	}
	for i := range m.marketSecOffset {
		m.marketSecOffset[i] = min(max(m.marketSecOffset[i], 0),
			max(secs[i].count-budgets[i], 0))
	}
}
