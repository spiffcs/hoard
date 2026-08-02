package browse

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

// ArbitrageFunc fetches today's vendor quotes and ranks them.
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
type ArbitrageFunc func(ctx context.Context, p progress.Fn) (arbitrage.Result, error)

// arbitrageRows is how many rows each of the three questions contributes.
const arbitrageRows = 15

// arbitrageMsg carries a finished fetch back into the update loop.
type arbitrageMsg struct {
	gen int // which request this answers; a stale one is discarded
	res arbitrage.Result
	err error
}

// startArbitrage kicks off a fetch, or explains why it cannot.
//
// The generation counter is what makes leaving and re-entering the view safe: a
// reply from an abandoned request arrives with an old gen and is dropped, rather
// than overwriting rows the user has since asked for something else.
func (m *Model) startArbitrage() tea.Cmd {
	if m.arbitrage == nil {
		m.status, m.statusErr = "arbitrage is unavailable in this build", true
		return nil
	}
	if m.arbLoading {
		// The key must never be silent (observed live: silence taught the
		// user F was broken).
		m.status, m.statusErr = "already fetching vendor prices — esc cancels", false
		return nil
	}
	if m.arbLoaded {
		// A repeat F re-asks: quotes are cached for the day, so the refresh
		// is cheap, and the user pressed the key on purpose.
		m.arbLoaded = false
	}

	m.cancelArbitrage()
	ctx, cancel := context.WithCancel(m.ctx)
	m.arbCancel = cancel
	m.arbLoading = true
	// Clear whatever the last keystroke left on the status line: it would
	// otherwise sit in front of the progress this fetch is about to report.
	m.status, m.statusErr = "", false
	m.arbGen++
	gen, fetch := m.arbGen, m.arbitrage

	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		res, err := fetch(ctx, nil)
		return arbitrageMsg{gen: gen, res: res, err: err}
	})
}

// onArbitrage receives a finished fetch.
func (m Model) onArbitrage(msg arbitrageMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.arbGen {
		return m, nil // a reply to a request the user has already left
	}
	m.arbLoading = false
	m.cancelArbitrage()

	if msg.err != nil {
		// Cancellation is the user leaving, not a failure worth shouting about.
		if m.ctx.Err() == nil && !isCanceled(msg.err) {
			m.setError(msg.err)
		}
		return m, nil
	}
	m.arbResult = msg.res
	m.arbRows = arbitrage.Rows(msg.res, arbitrageRows)
	m.sortArbRows()
	m.arbLoaded = true
	m.status = ""
	m.clampCursor(paneCards)
	return m, nil
}

// cancelArbitrage stops any fetch in flight.
func (m *Model) cancelArbitrage() {
	if m.arbCancel != nil {
		m.arbCancel()
		m.arbCancel = nil
	}
}

// isCanceled reports whether an error is just the context being cancelled —
// the user leaving the view, not something worth putting on screen.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// arbitrageLines renders the ranked opportunities as the CLI's three
// sections, stacked in one scrolling pane: each kind gets its own title
// row and its own column headers, because the three ask different
// questions and one shared header row was reduced to lying about at least
// two of them (a "GAIN" over a buylist haircut, observed live). The
// cursor still walks data rows only; section furniture scrolls past it.
func (m Model) arbitrageLines(width int) []string {
	if width <= 0 || len(m.arbRows) == 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: true, Clamp: true}

	var all []string
	rowLine := make([]int, 0, len(m.arbRows))
	flush := func(kind arbitrage.Kind, rows []arbitrage.Row) {
		if len(rows) == 0 {
			return
		}
		if len(all) > 0 {
			all = append(all, "")
		}
		all = append(all, titleStyle.Render(kind.Title())+"  "+helpStyle.Render(kind.Note()))
		t := arbSectionTable(env, kind, rows)
		t.Env, t.Header = env, true
		lines := t.Lines()
		all = append(all, lines[0])
		for _, line := range lines[1:] {
			rowLine = append(rowLine, len(all))
			all = append(all, line)
		}
	}
	// m.arbRows is already in section order (profit, liquid, spread).
	var section []arbitrage.Row
	var kind arbitrage.Kind
	for i, r := range m.arbRows {
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
	cur := min(max(m.cursor[paneCards], 0), len(m.arbRows)-1)
	cline := rowLine[cur]
	style := inactiveStyle
	if m.focus == paneCards {
		style = cursorStyle
	}
	all[cline] = style.Render(ansi.Strip(fit(all[cline], width)))

	visible := m.visibleRows()
	startAt := 0
	if cline >= visible {
		startAt = cline - visible + 1
	}
	return all[startAt:min(startAt+visible, len(all))]
}

// arbSectionTable lays out one kind's rows with headers that mean what
// they say for that kind alone.
func arbSectionTable(env ui.Env, kind arbitrage.Kind, rows []arbitrage.Row) ui.Table {
	name := ui.Col{Title: "NAME", Align: ui.Left, Flex: true, Min: 10}
	setNum := ui.Col{Title: "SET/NUM", Align: ui.Left, Priority: 6, Style: env.Dim()}
	fin := ui.Col{Title: "FIN", Align: ui.Left, Priority: 5, Style: env.Dim()}
	vendor := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Left, Priority: 4, Style: env.Dim()} }
	money := func(t string) ui.Col { return ui.Col{Title: t, Align: ui.Right} }

	var t ui.Table
	switch kind {
	case arbitrage.KindProfit:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("BUY"), vendor("FROM"), money("SELL"), vendor("TO"), money("PROFIT")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.BuyAt)), ui.C(r.BuyFrom),
				ui.C(ui.Money(r.SellAt)), ui.C(r.SellTo), ui.C("+"+ui.Money(r.Profit())))
		}
	case arbitrage.KindLiquid:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("RETAIL"), money("BUYLIST"), vendor("TO"), money("PAYS")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.BuyAt)), ui.C(ui.Money(r.SellAt)), ui.C(r.SellTo),
				ui.C(ui.Percent(r.Liquidity())))
		}
	default:
		t = ui.Table{Cols: []ui.Col{name, setNum, fin,
			money("LOW"), vendor("AT"), money("HIGH"), vendor("AT"), money("APART")}}
		for _, r := range rows {
			t.Add(ui.C(r.Card.Name), ui.C(r.Printing()), ui.C(ui.Finish(r.Card.Finish)),
				ui.C(ui.Money(r.BuyAt)), ui.C(r.BuyFrom),
				ui.C(ui.Money(r.DearAt)), ui.C(r.DearFrom), ui.C("+"+ui.Percent(r.Spread())))
		}
	}
	return t
}

// arbitrageHeader is the right pane's title and summary while in this view.
func (m Model) arbitrageHeader() (title, totals string) {
	switch {
	case m.arbLoading:
		return "ARBITRAGE", "asking vendors…"
	case !m.arbLoaded:
		return "ARBITRAGE", "press F to fetch"
	}
	return "ARBITRAGE", fmt.Sprintf("%s rows · %s printings compared",
		ui.Count(len(m.arbRows)), ui.Count(m.arbResult.Compared))
}

// arbitrageStatus is the status line for this view, covering the three states
// the pane can be in before it has rows.
func (m Model) arbitrageStatus() string {
	switch {
	case m.arbLoading:
		return m.spinner.View() + " reading today's vendor prices (first read of the day downloads ~5 MB)"
	case !m.arbLoaded:
		return helpStyle.Render(
			"arbitrage may need a download, so it waits to be asked — press F")
	case len(m.arbRows) == 0:
		return helpStyle.Render("no vendor disagreed about anything you own today")
	}
	// The status line explains the selected row's question — the flat list
	// has no section headers, so without this a liquid row's percentage
	// reads as a gain when it is the size of the haircut.
	return helpStyle.Render(fmt.Sprintf("%d/%d · %s · one-day vendor prices, not guaranteed sales",
		m.cursor[paneCards]+1, len(m.arbRows), m.selectedArbNote()))
}

// selectedArbNote is one sentence on why the row under the cursor is
// listed, in that row's own numbers.
func (m Model) selectedArbNote() string {
	i := m.cursor[paneCards]
	if i < 0 || i >= len(m.arbRows) {
		return ""
	}
	r := m.arbRows[i]
	switch r.Kind {
	case arbitrage.KindProfit:
		return fmt.Sprintf("%s pays %s · retail is %s at %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.BuyAt), r.BuyFrom)
	case arbitrage.KindLiquid:
		return fmt.Sprintf("%s pays %s · retail is %s",
			r.SellTo, ui.Money(r.SellAt), ui.Money(r.BuyAt))
	}
	return fmt.Sprintf("%s asks %s · %s asks %s",
		r.BuyFrom, ui.Money(r.BuyAt), r.DearFrom, ui.Money(r.DearAt))
}
