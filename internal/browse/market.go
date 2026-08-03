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

// MarketFunc fetches today's vendor quotes and ranks them.
//
// Injected rather than implemented here. It needs the MTGJSON id resolver,
// which writes learned ids back to the catalog and is shared with
// update-prices; duplicating that inside a UI package would put a second copy of
// a cache-writing download path somewhere nobody would look for it. This way
// internal/browse has no network dependency at all — and a test can supply one
// that returns instantly, fails, or blocks forever.
//
// It must honour ctx: the browser cancels it when the user leaves the view.
// The progress callback follows the action layer's contract; the browser
// passes nil today and gains a live consumer with the op layer.
type MarketFunc func(ctx context.Context, p progress.Fn) (market.Result, error)

// marketRowLimit is how many rows each table contributes. Generous, not
// unbounded: the fixed regions scroll, so the tail of a ranking (the 70%
// pays rows, the cheaper comps) is reachable — but a table is still a
// ranked answer, not a dump of everything compared.
const marketRowLimit = 50

// marketMsg carries a finished fetch back into the update loop.
type marketMsg struct {
	gen int // which request this answers; a stale one is discarded
	res market.Result
	err error
}

// startMarketFetch kicks off a fetch, or explains why it cannot.
//
// The generation counter is what makes leaving and re-entering the view safe: a
// reply from an abandoned request arrives with an old gen and is dropped, rather
// than overwriting rows the user has since asked for something else.
func (m *Model) startMarketFetch() tea.Cmd {
	if m.marketFetch == nil {
		m.status, m.statusErr = "vendor quotes are unavailable in this build", true
		return nil
	}
	if m.marketLoading {
		// The key must never be silent (observed live: silence taught the
		// user F was broken).
		m.status, m.statusErr = "already fetching vendor prices · esc cancels", false
		return nil
	}
	if m.marketLoaded {
		// A repeat F re-asks: quotes are cached for the day, so the refresh
		// is cheap, and the user pressed the key on purpose.
		m.marketLoaded = false
	}

	m.cancelMarketFetch()
	ctx, cancel := context.WithCancel(m.ctx)
	m.marketCancel = cancel
	m.marketLoading = true
	// Clear whatever the last keystroke left on the status line: it would
	// otherwise sit in front of the progress this fetch is about to report.
	m.status, m.statusErr = "", false
	m.marketGen++
	gen, fetch := m.marketGen, m.marketFetch

	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		res, err := fetch(ctx, nil)
		return marketMsg{gen: gen, res: res, err: err}
	})
}

// onMarket receives a finished fetch.
func (m Model) onMarket(msg marketMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.marketGen {
		return m, nil // a reply to a request the user has already left
	}
	m.marketLoading = false
	m.cancelMarketFetch()

	if msg.err != nil {
		// Cancellation is the user leaving, not a failure worth shouting about.
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

// cancelMarketFetch stops any fetch in flight.
func (m *Model) cancelMarketFetch() {
	if m.marketCancel != nil {
		m.marketCancel()
		m.marketCancel = nil
	}
}

// isCanceled reports whether an error is just the context being cancelled —
// the user leaving the view, not something worth putting on screen.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// MarketCachedFunc serves today's already-fetched quotes without the
// network, or ok=false when there is nothing cached.
type MarketCachedFunc func() (market.Result, bool)

// loadCachedMarket populates the view from an earlier session's fetch
// when one exists, so restarting the program does not blank the tables. F
// still re-asks for fresh numbers.
func (m *Model) loadCachedMarket() {
	if m.marketCached == nil || m.marketLoaded || m.marketLoading {
		return
	}
	res, ok := m.marketCached()
	if !ok {
		return
	}
	m.marketResult = res
	m.applyMarketRows()
	m.marketLoaded = true
	m.status, m.statusErr = "vendor quotes from earlier today · F refetches", false
}

// marketLines renders the four tables in fixed regions: each section gets
// a share of the height (marketSectionBudgets) and scrolls its own rows
// inside it, rather than the pane scrolling as one document — so the
// three-chart shape holds still while the cursor digs into any one of
// them. Each section keeps its own title row and column headers, because
// the tables ask different questions and one shared header row was reduced
// to lying about at least two of them (a "GAIN" over a buylist haircut,
// observed live). An overflowing section says where it is in its rows on
// the title line; an empty one keeps its title over a note, so a table
// emptied by the container filter reads as filtered, not missing.
func (m Model) marketLines(width int) []string {
	_, filtered := m.filterContainerID()
	if width <= 0 {
		return nil
	}
	if m.marketTotalRows() == 0 && !(m.marketLoaded && filtered) {
		// Not loaded (the status line explains the fetch states), or loaded
		// and genuinely empty hoard-wide.
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	secs := m.marketSections()
	budgets := m.marketSectionBudgets()
	// The bar only draws while the pane has focus — same reasoning as the
	// generic windowing: an Inactive mark on an analytical row reads as a
	// dimmed card, not a remembered place.
	cursorSec, cursorIdx := m.marketCursorPos()
	hasCursor := m.focus == paneCards
	emptyNote := "nothing today"
	if filtered {
		emptyNote = "none in this collection"
	}

	var out []string
	for i, sec := range secs {
		if i > 0 {
			out = append(out, "")
		}
		title, note := market.CompsTitle+" · SELL", compsSellNote
		if m.compsBuySide {
			title, note = market.CompsTitle+" · BUY", compsBuyNote
		}
		if i != compsSection {
			title, note = market.Kind(i).Title(), market.Kind(i).Note()
		}
		head := m.theme.Title.Render(title) + "  " + m.theme.Help.Render(note)
		if off := m.marketSecOffset[i]; sec.count > budgets[i] && budgets[i] > 0 {
			head += m.theme.Help.Render(fmt.Sprintf(" · %d–%d of %d",
				off+1, off+budgets[i], sec.count))
		}
		out = append(out, head)
		if sec.count == 0 {
			out = append(out, m.theme.Help.Render(emptyNote))
			continue
		}

		var t ui.Table
		if i == compsSection {
			t = compsSectionTable(env, m.marketComps, m.compsBuySide)
		} else {
			t = marketSectionTable(env, market.Kind(i), m.marketRows[sec.start:sec.start+sec.count])
		}
		t.Env, t.Header = env, true
		lines := t.Lines()
		out = append(out, lines[0])
		off := m.marketSecOffset[i]
		for r := off; r < min(off+budgets[i], sec.count); r++ {
			line := lines[1+r]
			if hasCursor && i == cursorSec && r == cursorIdx {
				// Restyle, not strip: the bar spans the row and the identity
				// tints show through it, same as the generic pane windowing.
				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

// marketSectionTable lays out one kind's rows with headers that mean what
// they say for that kind alone.
func marketSectionTable(env ui.Env, kind market.Kind, rows []market.Row) ui.Table {
	name := ui.Col{Title: "NAME", Align: ui.Left, Flex: true, Min: 10}
	setNum := ui.Col{Title: "SET/NUM", Align: ui.Left, Priority: 6, Style: env.Dim()}
	fin := ui.Col{Title: "FIN", Align: ui.Left, Priority: 5, Style: env.Dim()}
	vendor := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Left, Priority: 4, Style: env.Dim()} }
	money := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Right} }
	// The name block every section shares: tinted name, printing, finish.
	// No pips column here — the market tables are dense with money, and
	// the name's identity tint already carries the color story.
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
			money("TCG SOLD"), money("BUYLIST"), vendor("TO"), money("PROFIT")}}
		for _, r := range rows {
			// A profit is the one genuine gain on this screen; the ratios in
			// the other sections stay uncolored — a below-market discount in
			// red would read as a loss when it is a reason to buy.
			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)), ui.C(r.SellTo),
				ui.Cell{Text: "+" + ui.Money(r.Profit()), Style: env.Gain()})...)
		}
	case market.KindLiquid:
		// No TO column: Card Kingdom runs the only buylist in the feed, so
		// a vendor column repeated one name down every row. The status note
		// still names the bidder, and the column comes back the day a
		// second buylist source exists.
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("TCG SOLD"), money("BUYLIST"), money("PAYS")}}
		for _, r := range rows {
			// The ratio columns grade on a color ramp — how close to the
			// section's ideal, not a gain/loss direction.
			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)),
				ui.Cell{Text: ui.Percent(r.Liquidity()),
					Style: env.Grade(market.LiquidityGrade(r.Liquidity()))})...)
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

// marketHeader is the right pane's title and summary while in this view.
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

// marketStatus is the status line for this view, covering the three states
// the pane can be in before it has rows.
func (m Model) marketStatus() string {
	switch {
	case m.marketLoading:
		return m.spinner.View() + " reading today's vendor prices (first read of the day downloads ~5 MB)"
	case !m.marketLoaded:
		return m.theme.Help.Render(
			"vendor quotes may need a download, so the view waits to be asked · press F")
	case m.marketTotalRows() == 0:
		if sel := m.selectedContainer(); sel != nil && sel.Kind != kindAllCards {
			return m.theme.Help.Render(fmt.Sprintf(
				"no market rows in %s · All cards shows every container", sel.Name))
		}
		return m.theme.Help.Render("no vendor disagreed about anything you own today")
	}
	// The status line explains the selected row's question — the flat list
	// has no section headers, so without this a liquid row's percentage
	// reads as a gain when it is the size of the haircut.
	// Position within the cursor's own table, not the flat row space: a
	// reader on the comps sheet is 1/50 of the comps, wherever the other
	// tables' counts happen to sit.
	secs := m.marketSections()
	sec, idx := m.marketCursorPos()
	// On the comp sheets the standing note teaches that side's SPREAD
	// formula instead — the column is each sheet's one derived number,
	// and the two sides derive different things.
	suffix := "one-day vendor prices"
	if m.selectedComp() != nil {
		suffix = "SPREAD = high sale minus low sale, over high"
		if m.compsBuySide {
			suffix = "SPREAD = 1 − BUYLIST ÷ LOW"
		}
	}
	return m.theme.Help.Render(fmt.Sprintf("%d/%d · %s · %s",
		idx+1, secs[sec].count, m.selectedMarketNote(), suffix))
}

// selectedMarketNote is one sentence on why the row under the cursor is
// listed, in that row's own numbers.
func (m Model) selectedMarketNote() string {
	// A comps row explains the side it is showing, in that side's own
	// terms — a sell note quoting a low ask read as apples against
	// oranges (observed live).
	if c := m.selectedComp(); c != nil {
		if m.compsBuySide {
			// The buy side is the bid and its haircut.
			if !c.HasBuylist {
				return "no buylist bid today"
			}
			note := c.BuylistTo + " pays " + ui.Money(c.Buylist)
			if c.HasSpread() {
				note += " · spread " + ui.PercentAlways(c.Spread())
			}
			return note
		}
		// The sell side reads the sale prices out venue by venue.
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
	i := m.cursor[paneCards]
	if i < 0 || i >= len(m.marketRows) {
		return ""
	}
	r := m.marketRows[i]
	switch r.Kind {
	case market.KindProfit:
		return fmt.Sprintf("%s pays %s · tcg last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	case market.KindLiquid:
		return fmt.Sprintf("%s pays %s · tcg last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	}
	return fmt.Sprintf("%s asks %s · tcg last sold for %s",
		r.BuyFrom, ui.Money(r.BuyAt), ui.Money(r.Market))
}

// applyMarketRows derives the visible rows from the last result: scoped to
// the selected container first — the filter runs before the per-section
// ranking, so a deck gets its own top rows rather than whatever survived
// the hoard-wide cut — then ranked, floor-filtered on per-copy value, and
// re-sorted the way the user left it.
func (m *Model) applyMarketRows() {
	res := m.marketResult
	// scopeOwned rescales a row's card to the copies the selected container
	// holds — VALUE follows QTY so per-copy figures (unit price, floor)
	// stay intact while the totals describe the container, not the hoard.
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
	// The set scope, mutually exclusive with the container scope, filters
	// the same way: before ranking, so a set gets its own top rows.
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
	rows := market.Rows(res, marketRowLimit)
	kept := rows[:0]
	for _, r := range rows {
		// The browser dropped its BELOW MARKET table — the space serves the
		// comps better (owner's call, dogfooding). The CLI still prints the
		// kind, so the analysis itself is untouched; only this surface
		// filters it out, and marketSections depends on the kinds here
		// never reaching compsSection's slot.
		if r.Kind == market.KindBelowMarket {
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
	m.marketRows = kept
	m.sortArbRows()
	m.applyMarketComps(res.Comps)
	// The rows under the old scroll positions are gone; every section
	// starts back at its top.
	m.marketSecOffset = [3]int{}
}

// marketSectionBudgets divides the pane's rows among the four sections:
// equal shares, with a section that needs less than its share donating the
// slack to the ones that need more. Deterministic, sums to at most the
// pool, and never exceeds a section's own row count.
func (m Model) marketSectionBudgets() [3]int {
	secs := m.marketSections()
	// Furniture: a separator above each section but the first, and a title
	// plus one line (the column header, or the empty note) per section.
	pool := max(m.visibleRows()-(2+3*2), 0)
	var budget [3]int
	var active []int
	for i, s := range secs {
		if s.count > 0 {
			active = append(active, i)
		}
	}
	for len(active) > 0 {
		share := pool / len(active)
		if share == 0 {
			break
		}
		kept := active[:0]
		satisfied := false
		for _, i := range active {
			if secs[i].count <= share {
				budget[i] = secs[i].count
				pool -= secs[i].count
				satisfied = true
				continue
			}
			kept = append(kept, i)
		}
		active = kept
		if satisfied {
			continue // the slack returns to the pool; re-share it
		}
		// Everyone left is overfull: the share each, remainder one row at a
		// time in section order.
		for _, i := range active {
			budget[i] = share
			pool -= share
		}
		for _, i := range active {
			if pool == 0 {
				break
			}
			if budget[i] < secs[i].count {
				budget[i]++
				pool--
			}
		}
		break
	}
	// The cursor's section always shows at least its selected row, even at
	// a pathological height — steal one from the largest budget.
	sec, _ := m.marketCursorPos()
	if budget[sec] == 0 && secs[sec].count > 0 {
		big := 0
		for i := range budget {
			if budget[i] > budget[big] {
				big = i
			}
		}
		if budget[big] > 0 {
			budget[big]--
			budget[sec] = 1
		}
	}
	return budget
}

// jumpMarketSection moves the cursor to the first row of the next (+1) or
// previous (-1) non-empty market table — the ]/[ keys, so reaching COMPS
// never means walking three tables' overflow row by row.
func (m *Model) jumpMarketSection(dir int) {
	secs := m.marketSections()
	cur, _ := m.marketCursorPos()
	for i := cur + dir; i >= 0 && i < len(secs); i += dir {
		if secs[i].count == 0 {
			continue
		}
		m.cursor[paneCards] = secs[i].start
		// The jump is a card-pane gesture wherever the hand was.
		m.focus = paneCards
		m.scrollIntoView()
		return
	}
}

// scrollMarketIntoView keeps the cursor visible inside its section's
// region, and every section's offset inside its rows — the market pane's
// counterpart to the generic offset walk.
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
