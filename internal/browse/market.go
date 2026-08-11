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
// passes nil today and gains a live consumer with the op layer. min is the
// market floor: cards whose low ask sits under it are left out of the
// result (the browser owns the value — see marketFloor).
type MarketFunc func(ctx context.Context, p progress.Fn, min float64) (market.Result, error)

// defaultMarketFloor is the market view's price floor: rows whose low ask
// sits under it are noise, not opportunities — a 900% spread between $0.20
// and $1.99 is arithmetic. Matches the CLI command's --min default.
// TogglePennyFilter shows the bulk anyway; SetPennyFilter moves the line.
const defaultMarketFloor = 1.00

// singleTablePageSize is the holdings and movers page: a single table owns
// the whole pane there, so its page runs deeper than the market's
// three-way split before >/< is needed.
const singleTablePageSize = 59

// pageSize is how many rows each market table shows per page. Generous, not
// unbounded: the fixed regions scroll, so a page's tail (the 70% pays
// rows, the cheaper comps) is reachable — and >/< turn the page, so the
// full ranking is too, fifty rows at a time.
const pageSize = 50

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

	min := m.activeMarketFloor()
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		res, err := fetch(ctx, nil, min)
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
// network, re-collected against the given floor, or ok=false when there
// is nothing cached.
type MarketCachedFunc func(min float64) (market.Result, bool)

// loadCachedMarket populates the view from an earlier session's fetch
// when one exists, so restarting the program does not blank the tables. F
// still re-asks for fresh numbers.
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
	// This status replaces the arrival beat, so the armed floor's mention
	// must ride along — rows silently absent read as missing data.
	if !m.marketPennies {
		m.status += " · penny filter < " + ui.Money(m.marketFloor)
	}
}

// activeMarketFloor is the floor the next collection uses: 0 while the
// penny filter is off — everything with two vendors shows.
func (m Model) activeMarketFloor() float64 {
	if m.marketPennies {
		return 0
	}
	return m.marketFloor
}

// refreshMarketFloor re-collects the loaded result under the current
// floor. The day-cache answers instantly, so moving the line never costs a
// download; if the cache is gone (the day rolled over), the view asks for
// F rather than showing rows filtered by a line the user just moved.
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
	if m.marketTotalRows() == 0 && (!m.marketLoaded || (!filtered && m.filter.empty())) {
		// Not loaded (the status line explains the fetch states), or loaded
		// and genuinely empty hoard-wide. A query counts alongside the
		// container filter here: emptied by something the reader typed, the
		// screen has to keep its headings and say so, not go blank.
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	secs := m.marketSections()
	budgets := m.marketSectionBudgets()
	totals := m.marketSectionTotals()
	// The bar only draws while the pane has focus — same reasoning as the
	// generic windowing: an Inactive mark on an analytical row reads as a
	// dimmed card, not a remembered place.
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
			// The heading is the selection when there is nothing under it,
			// so the bar goes on the heading itself — the table's own keys
			// ('b', 's') act on whatever the bar is sitting on, and without
			// it an empty table would look unreachable while answering them.
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
				// Restyle, not strip: the bar spans the row and the identity
				// tints show through it, same as the generic pane windowing.
				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

// marketEmptyNote is one table's reason for being empty: a refused term
// first, then the query, then the container filter, then the standing
// reason. The middle three are the precedence the watches screen established
// (watchEmptyNote), for its reason — "nothing today" is a claim about what
// the vendors did, and it would be a lie about the hoard when a typed query
// is what emptied the table, reading as a day with no arbitrage in it and
// sending the reader to F to refetch prices they already have.
func (m Model) marketEmptyNote(filtered bool) string {
	// A refused term outranks the query that carried it, and it is the one
	// rung this chain has that the watches screen's does not need. Emptied by
	// price<5, "none match price<5" claims no card on this screen costs under
	// five dollars — a lie about the hoard, and a worse one than the standing
	// note would have been, since the term was declined rather than answered.
	// The bar's own sentence, verbatim: the reader saw it a keystroke ago and
	// a paraphrase here would read as a second, different objection.
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

// marketSectionHead is one table's heading and its explanatory note, in
// the state the table is currently showing — which for the two flippable
// tables is not the same thing as the section index alone.
func (m Model) marketSectionHead(i int) (title, note string) {
	if i == compsSection {
		if m.compsBuySide {
			return market.CompsTitle + " · BUY", compsBuyNote
		}
		return market.CompsTitle + " · SELL", compsSellNote
	}
	kind := market.Kind(i)
	// The lowball rows travel as KindLiquid, so the heading is the one
	// place that has to name the band the section is actually showing.
	if kind == market.KindLiquid && m.liquidLowball {
		kind = market.KindLowball
	}
	return kind.Title(), kind.Note()
}

// marketSectionTable lays out one kind's rows with headers that mean what
// they say for that kind alone. lowball selects the buylist table's band,
// which shares the kind's columns and changes only how PAYS is painted.
func marketSectionTable(env ui.Env, kind market.Kind, rows []market.Row, lowball bool) ui.Table {
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
		// No TO column, and the buylist names its bidder in the header —
		// Card Kingdom runs the only buylist in the feed, so a vendor cell
		// repeated one name down every row (same reasoning as the liquid
		// section below).
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("TCG SOLD"), money("CK BUYLIST"), money("PROFIT")}}
		for _, r := range rows {
			// A profit is the one genuine gain on this screen; the ratios in
			// the other sections stay uncolored — a below-market discount in
			// red would read as a loss when it is a reason to buy.
			t.Add(append(cardCells(r),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)),
				ui.Cell{Text: "+" + ui.Money(r.Profit()), Style: env.Gain()})...)
		}
	case market.KindLiquid:
		// No TO column: Card Kingdom runs the only buylist in the feed, so
		// a vendor column repeated one name down every row. The status note
		// still names the bidder, and the column comes back the day a
		// second buylist source exists.
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("TCG SOLD"), money("CK BUYLIST"), money("PAYS")}}
		for _, r := range rows {
			// The ratio columns grade on a color ramp — how close to the
			// section's ideal, not a gain/loss direction. The two bands read
			// opposite ideals off the same number, so they take opposite
			// ramps: green as the near-market rows approach full price, red
			// as the lowball rows fall away from it.
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
		// marketEmptyNote's chain, on the status line: a refused term, then
		// the query, then the container, then the standing reason. It has to
		// be the same order — the tables under this line are drawing their
		// own notes off that chain, and a status line reaching a different
		// rung would disagree with them in the same frame. The query outranks
		// the container because it is the most recent thing the reader did
		// and the one thing esc undoes.
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
	// The status line explains the selected row's question — the flat list
	// has no section headers, so without this a liquid row's percentage
	// reads as a gain when it is the size of the haircut.
	// Position within the cursor's own table, not the flat row space: a
	// reader on the comps sheet is 1/50 of the comps, wherever the other
	// tables' counts happen to sit.
	secs := m.marketSections()
	sec, idx := m.marketCursorPos()
	// On the comp sheets the standing note teaches that side's derived
	// column instead — each sheet has exactly one, and the two sides
	// derive different things (dispersion vs a true bid-ask spread).
	suffix := "one-day vendor prices"
	if m.selectedComp() != nil {
		suffix = "PRICE DISPERSION = high sale minus low sale, over high"
		if m.compsBuySide {
			suffix = "SPREAD = 1 − BUYLIST ÷ LOW"
		}
	} else if m.liquidLowball && sec == int(market.KindLiquid) {
		// The lowball band is the one table whose heading is not the default,
		// so the standing note says 'b' takes it back — otherwise a reader
		// who flipped it by accident has no way to read the way out.
		suffix = "lowball band · 'b' for the shops paying near market"
	}
	// On an empty heading there is no Nth-of-M to report and no row note to
	// print: the table itself is the selection, so the line names it.
	title, _ := m.marketSectionHead(sec)
	line := fmt.Sprintf("%s · empty", title)
	if secs[sec].count > 0 {
		line = fmt.Sprintf("%d/%d · %s", idx+1, secs[sec].count, m.selectedMarketNote())
	}
	// The selection leads here too — the container on the left, the row's
	// card on the right — matching the generic position line.
	if name := m.selectedItemName(); name != "" {
		line = name + " · " + line
	}
	// A live query goes ahead of the standing note, where the holdings and
	// watches lines put it last. Measured, not preferred: this line alone
	// carries a teaching suffix and already runs past a 100-column pane
	// unclamped, so the tail is the part that falls off the edge. Once the
	// bar closes this phrase is the only thing on screen saying the tables
	// hold a subset, while the suffix explains a column the reader can see
	// either way — so the perishable half takes the safe slot.
	if !m.filter.empty() {
		line += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	return m.theme.Help.Render(line + " · " + suffix)
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
	// A heading with no rows under it explains itself in the section note;
	// there is no row here to read numbers off.
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
		// "only" is the whole difference: the same three numbers read as a
		// fair offer in one band and an insulting one in the other, and the
		// row should say which it is without the reader doing the division.
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
	// The typed query last, so it sees the container-scoped Copies and Value
	// the blocks above rewrote and `qty` describes the same copies the rest of
	// the screen does — the order movers and the watches screen apply it in.
	// Before the ranking, like the scopes: a query gets its own top rows,
	// rather than whatever survived the hoard-wide cut and happens to match.
	//
	// One projection and one matcher for both row types (see marketAsCard).
	// A second filter beside filter.matches is how this bug family started,
	// and the two lists would answer the same query differently.
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
	// The whole ranking, not a top-N: paging owns the truncation now, so
	// every section keeps its full run and shows pageSize at a time.
	rows := market.Rows(res, len(res.Opportunities))
	if m.liquidLowball {
		// The band is a swap, not a fourth table: the lowball rows keep
		// KindLiquid so the section geometry, paging and per-section sort
		// state — all [3]-wide and indexed by Kind — stay untouched. Order
		// here does not matter, sortArbRows groups by Kind first.
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
		// The browser dropped its BELOW MARKET table — the space serves the
		// comps better (owner's call, dogfooding). The CLI still prints the
		// kind, so the analysis itself is untouched; only this surface
		// filters it out, and marketSections depends on the kinds here
		// never reaching compsSection's slot. Stated as "everything but the
		// two rendered kinds" rather than naming KindBelowMarket: the
		// downstream [3] state indexes by Kind, so any kind market ever
		// grows past 2 (KindLowball already exists at 3) must die here, not
		// as an index-out-of-range in the render.
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
	// A new collection means new rankings: every section back to page one.
	m.marketPage = [3]int{}
	m.sortArbRows()
	m.applyMarketComps(res.Comps)
	// The rows under the old scroll positions are gone; every section
	// starts back at its top.
	m.marketSecOffset = [3]int{}
	// And a cursor left on a table that no longer has rows moves to one
	// that does — an empty heading is somewhere to navigate to, not
	// somewhere to be put. Callers that mean to stay (the band flip) park
	// the cursor themselves afterwards.
	sec, _ := m.marketCursorPos()
	if m.marketSections()[sec].count == 0 {
		m.cursor[paneCards] = m.firstMarketCursor()
	}
}

// marketSectionTotals is each section's full row count across every page —
// the visible marketSections count only the current page.
func (m Model) marketSectionTotals() [3]int {
	var t [3]int
	for _, r := range m.marketAllRows {
		// applyMarketRows admits only the two rendered kinds; this keeps the
		// count total anyway, because the price of a stray Kind reaching an
		// index expression here is a panic mid-render.
		if r.Kind < 0 || int(r.Kind) >= compsSection {
			continue
		}
		t[r.Kind]++
	}
	t[compsSection] = len(m.marketAllComps)
	return t
}

// deriveMarketPages slices the visible page of every section out of the
// full rankings, clamping each page to what its section actually has.
// Runs after anything that reorders or replaces the full lists.
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

// pagePhrase is the heading fragment every paged table hangs off its
// title: two distinct count phrases — "7–26 of 47" scopes the screen
// (which of this page's rows are visible), "51–97 of 97" scopes the page
// (which slice of the whole ranking it holds, advancing on >/<) — each
// present only when it says something. Chaining them into one "of 47 of
// 97" read as a typo; >/< lives in the help line, not repeated here.
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

// tablePagePhrase is pagePhrase fed from the single-table panes' shared
// window state — the generic offset and screen budget the holdings and
// movers panes scroll with.
func (m Model) tablePagePhrase(count, page, tot int) string {
	return pagePhrase(m.offset[paneCards], max(m.visibleRows()-1, 0), count, page, tot,
		singleTablePageSize)
}

// turnTablePage turns the current view's paged table: the market's own
// turner for its three tables, the shared single-table turn for the
// holdings pane and movers.
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

// turnOnePage is turnMarketPage's single-table sibling: clamp, name the
// edges, land the cursor on the new page's first row.
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
	// The sort rides along: which slice rows 60–118 are depends entirely on
	// the order, and page-deep leafing is exactly when that stops being
	// remembered.
	m.status, m.statusErr = fmt.Sprintf("page %d/%d · rows %d–%d of %d · sorted by %s",
		next+1, maxPage+1, next*singleTablePageSize+1,
		min((next+1)*singleTablePageSize, tot), tot, m.sortLabel()), false
}

// turnMarketPage turns the cursor's table one page forward (+1) or back
// (-1), landing the cursor on the new page's first row. At either end the
// status says so rather than staying silent.
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

// marketSectionBudgets divides the pane's rows among the three sections;
// the arithmetic is sectionBudgets, shared with the watches pane.
func (m Model) marketSectionBudgets() [3]int {
	secs := m.marketSections()
	counts := make([]int, len(secs))
	for i, s := range secs {
		counts[i] = s.count
	}
	// Furniture: a separator above each section but the first, and a title
	// plus one line (the column header, or the empty note) per section.
	pool := max(m.visibleRows()-(2+3*2), 0)
	sec, _ := m.marketCursorPos()
	var budget [3]int
	copy(budget[:], sectionBudgets(counts, pool, sec))
	return budget
}

// jumpMarketSection moves the cursor to the first row of the next (+1) or
// previous (-1) non-empty market table — the ]/[ keys, so reaching COMPS
// never means walking three tables' overflow row by row.
func (m *Model) jumpMarketSection(dir int) {
	secs := m.marketSections()
	cur, _ := m.marketCursorPos()
	for i := cur + dir; i >= 0 && i < len(secs); i += dir {
		// Empty tables are stops too: they keep their heading, and their
		// heading is where the table's own keys land.
		m.cursor[paneCards] = secs[i].curStart
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
