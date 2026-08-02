package browse

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
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

// marketRowLimit is how many rows each of the three questions contributes.
const marketRowLimit = 15

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
		m.status, m.statusErr = "already fetching vendor prices — esc cancels", false
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
	m.status, m.statusErr = "vendor quotes from earlier today — F refetches", false
}

// marketLines renders the ranked opportunities as the CLI's three
// sections, stacked in one scrolling pane: each kind gets its own title
// row and its own column headers, because the three ask different
// questions and one shared header row was reduced to lying about at least
// two of them (a "GAIN" over a buylist haircut, observed live). The
// cursor still walks data rows only; section furniture scrolls past it.
func (m Model) marketLines(width int) []string {
	if width <= 0 || len(m.marketRows) == 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}

	var all []string
	rowLine := make([]int, 0, len(m.marketRows))
	flush := func(kind market.Kind, rows []market.Row) {
		if len(rows) == 0 {
			return
		}
		if len(all) > 0 {
			all = append(all, "")
		}
		all = append(all, m.theme.Title.Render(kind.Title())+"  "+m.theme.Help.Render(kind.Note()))
		t := marketSectionTable(env, kind, rows)
		t.Env, t.Header = env, true
		lines := t.Lines()
		all = append(all, lines[0])
		for _, line := range lines[1:] {
			rowLine = append(rowLine, len(all))
			all = append(all, line)
		}
	}
	// m.marketRows is already in section order (profit, liquid, spread).
	var section []market.Row
	var kind market.Kind
	for i, r := range m.marketRows {
		if i == 0 || r.Kind != kind {
			flush(kind, section)
			section, kind = section[:0], r.Kind
		}
		section = append(section, r)
	}
	flush(kind, section)

	// Cursor highlight on the selected data row, then a line window that
	// keeps it visible — section furniture means rows and lines no longer
	// map 1:1, so the generic pane windowing cannot be reused here.
	cur := min(max(m.cursor[paneCards], 0), len(m.marketRows)-1)
	cline := rowLine[cur]
	style := m.theme.Inactive
	if m.focus == paneCards {
		style = m.theme.Cursor
	}
	all[cline] = style.Render(ansi.Strip(fit(all[cline], width)))

	visible := m.visibleRows()
	startAt := 0
	if cline >= visible {
		startAt = cline - visible + 1
	}
	return all[startAt:min(startAt+visible, len(all))]
}

// marketSectionTable lays out one kind's rows with headers that mean what
// they say for that kind alone.
func marketSectionTable(env ui.Env, kind market.Kind, rows []market.Row) ui.Table {
	name := ui.Col{Title: "NAME", Align: ui.Left, Flex: true, Min: 10}
	setNum := ui.Col{Title: "SET/NUM", Align: ui.Left, Priority: 6, Style: env.Dim()}
	fin := ui.Col{Title: "FIN", Align: ui.Left, Priority: 5, Style: env.Dim()}
	vendor := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Left, Priority: 4, Style: env.Dim()} }
	money := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Right} }

	var t ui.Table
	switch kind {
	case market.KindProfit:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("LAST SOLD"), money("BUYLIST"), vendor("TO"), money("PROFIT")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)), ui.C(r.SellTo),
				ui.C("+"+ui.Money(r.Profit())))
		}
	case market.KindLiquid:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("LAST SOLD"), money("BUYLIST"), vendor("TO"), money("PAYS")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.Market)), ui.C(ui.Money(r.SellAt)), ui.C(r.SellTo),
				ui.C(ui.Percent(r.Liquidity())))
		}
	default:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("ASK"), vendor("AT"), money("LAST SOLD"), money("BELOW")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.BuyAt)), ui.C(r.BuyFrom),
				ui.C(ui.Money(r.Market)), ui.C("-"+ui.Percent(r.BelowMarket())))
		}
	}
	return t
}

// marketHeader is the right pane's title and summary while in this view.
func (m Model) marketHeader() (title, totals string) {
	switch {
	case m.marketLoading:
		return "MARKET", "asking vendors…"
	case !m.marketLoaded:
		return "MARKET", "press F to fetch"
	}
	return "MARKET", fmt.Sprintf("%s rows · %s printings compared",
		ui.Count(len(m.marketRows)), ui.Count(m.marketResult.Compared))
}

// marketStatus is the status line for this view, covering the three states
// the pane can be in before it has rows.
func (m Model) marketStatus() string {
	switch {
	case m.marketLoading:
		return m.spinner.View() + " reading today's vendor prices (first read of the day downloads ~5 MB)"
	case !m.marketLoaded:
		return m.theme.Help.Render(
			"vendor quotes may need a download, so the view waits to be asked — press F")
	case len(m.marketRows) == 0:
		return m.theme.Help.Render("no vendor disagreed about anything you own today")
	}
	// The status line explains the selected row's question — the flat list
	// has no section headers, so without this a liquid row's percentage
	// reads as a gain when it is the size of the haircut.
	return m.theme.Help.Render(fmt.Sprintf("%d/%d · %s · one-day vendor prices, not guaranteed sales",
		m.cursor[paneCards]+1, len(m.marketRows), m.selectedMarketNote()))
}

// selectedMarketNote is one sentence on why the row under the cursor is
// listed, in that row's own numbers.
func (m Model) selectedMarketNote() string {
	i := m.cursor[paneCards]
	if i < 0 || i >= len(m.marketRows) {
		return ""
	}
	r := m.marketRows[i]
	switch r.Kind {
	case market.KindProfit:
		return fmt.Sprintf("%s pays %s · it last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	case market.KindLiquid:
		return fmt.Sprintf("%s pays %s · it last sold for %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.Market))
	}
	return fmt.Sprintf("%s asks %s · it last sold for %s",
		r.BuyFrom, ui.Money(r.BuyAt), ui.Money(r.Market))
}

// applyMarketRows derives the visible rows from the last result: ranked,
// mask-filtered on per-copy value, then re-sorted the way the user left it.
func (m *Model) applyMarketRows() {
	rows := market.Rows(m.marketResult, marketRowLimit)
	if min := m.maskMin(); min > 0 {
		kept := rows[:0]
		for _, r := range rows {
			unit := r.Card.Value
			if r.Card.Copies > 1 {
				unit /= float64(r.Card.Copies)
			}
			if unit >= min {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	m.marketRows = rows
	m.sortArbRows()
}
