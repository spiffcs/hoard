package browse

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/arbitrage"
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
type ArbitrageFunc func(ctx context.Context) (arbitrage.Result, error)

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
	if m.arbLoaded || m.arbLoading {
		return nil
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
		res, err := fetch(ctx)
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

// arbitrageLines renders the ranked opportunities.
//
// One flat list rather than the CLI's three tables: a scrolling pane cannot show
// three at once without burying two of them, so the rows keep their section
// order — real arbitrage first — and a WHY column says which question each row
// answers.
//
// The money columns say different things per row, which is why they carry no
// fixed titles: on an arbitrage row it is what you would pay and what a shop
// pays; on a spread row it is the cheapest and dearest asking price. The FROM
// and TO columns name the vendors, so the numbers are never anonymous.
func (m Model) arbitrageLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "WHY", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 6, Style: env.Dim()},
			{Title: "BUY", Align: ui.Right},
			{Title: "FROM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "SELL", Align: ui.Right},
			{Title: "TO", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "GAIN", Align: ui.Right},
		}}
		for _, r := range m.arbRows {
			sell, to, gain := ui.Money(r.SellAt), r.SellTo, ""
			switch r.Kind {
			case arbitrage.KindProfit:
				gain = "+" + ui.Money(r.Profit())
			case arbitrage.KindLiquid:
				gain = ui.Percent(r.Liquidity())
			default:
				// A spread row compares two asking prices, not a buy and a sell.
				sell, to, gain = ui.Money(r.DearAt), r.DearFrom, "+"+ui.Percent(r.Spread())
			}
			t.Add(ui.C(r.Kind.String()), ui.C(r.Card.Name), ui.C(r.Printing()),
				ui.C(ui.Money(r.BuyAt)), ui.C(r.BuyFrom), ui.C(sell), ui.C(to), ui.C(gain))
		}
		return t
	})
}

// arbitrageHeader is the right pane's title and summary while in this view.
func (m Model) arbitrageHeader() (title, totals string) {
	switch {
	case m.arbLoading:
		return "ARBITRAGE", "asking vendors…"
	case !m.arbLoaded:
		return "ARBITRAGE", "press enter to fetch"
	}
	return "ARBITRAGE", fmt.Sprintf("%s rows · %s printings compared",
		ui.Count(len(m.arbRows)), ui.Count(m.arbResult.Compared))
}

// arbitrageStatus is the status line for this view, covering the three states
// the pane can be in before it has rows.
func (m Model) arbitrageStatus() string {
	switch {
	case m.arbLoading:
		return m.spinner.View() + " asking every vendor for today's prices (~5 MB)"
	case !m.arbLoaded:
		return helpStyle.Render(
			"arbitrage needs a download, so it waits to be asked — press enter")
	case len(m.arbRows) == 0:
		return helpStyle.Render("no vendor disagreed about anything you own today")
	}
	return helpStyle.Render(fmt.Sprintf(
		"%d/%d · %s listings ignored as unsupported · vendor prices for one day, not guaranteed sales",
		m.cursor[paneCards]+1, len(m.arbRows), ui.Count(m.arbResult.Ignored)))
}
