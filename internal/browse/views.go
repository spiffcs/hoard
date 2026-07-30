package browse

import (
	"fmt"
	"strconv"
	"time"

	"strings"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// viewMode is what the right pane is showing.
//
// Movers and unpriced are properties of the whole hoard rather than of the
// selected container, so in those modes the left pane keeps its cursor but stops
// driving the right one. The alternative — filtering them to the selected
// container — would make "what moved" mean something different depending on
// where a cursor happened to be sitting.
type viewMode int

const (
	viewHoldings viewMode = iota
	viewMovers
	viewUnpriced
	viewArbitrage
)

func (v viewMode) String() string {
	switch v {
	case viewMovers:
		return "movers"
	case viewUnpriced:
		return "unpriced"
	case viewArbitrage:
		return "arbitrage"
	}
	return "holdings"
}

func (v viewMode) next() viewMode { return (v + 1) % 4 }

// moversWindow is how far back the movers view looks. The same default as
// `hoard movers`, so the two agree.
const moversWindow = 30 * 24 * time.Hour

// loadView reads whichever analysis the right pane is showing.
//
// Both are plain database reads and return in milliseconds, so they are loaded
// synchronously like the holdings. Nothing here touches the network.
func (m *Model) loadView() error {
	switch m.view {
	case viewMovers:
		since := m.now().Add(-moversWindow).UTC().Format(time.RFC3339)
		changes, err := m.store.Movers(since)
		if err != nil {
			return fmt.Errorf("reading movers: %w", err)
		}
		m.movers = store.MoversByImpact(changes)
		return nil
	case viewUnpriced:
		rows, err := m.store.Unpriced()
		if err != nil {
			return fmt.Errorf("reading unpriced cards: %w", err)
		}
		m.unpriced = rows
		return nil
	}
	return nil
}

// now is a var so tests can pin the movers window rather than depending on when
// they run.
func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// viewRowCount is how many rows the current right-pane mode holds.
func (m Model) viewRowCount() int {
	switch m.view {
	case viewMovers:
		return len(m.movers)
	case viewUnpriced:
		return len(m.unpriced)
	case viewArbitrage:
		return len(m.arbRows)
	}
	return len(m.cards)
}

// moversLines renders the risers and sinkers.
func (m Model) moversLines(width int) []string {
	env := ui.Env{Width: width, Color: true, Clamp: true}
	t := ui.Table{
		Env: env, Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "IMPACT", Align: ui.Right},
		},
	}
	for _, c := range m.movers {
		finish := c.Finish
		if finish == "normal" {
			finish = "-"
		}
		t.Add(ui.C(c.Name), ui.C(c.SetCode+"/"+c.CollectorNumber), ui.C(finish),
			ui.C(ui.Money(c.Old)), ui.C(ui.Money(c.New)),
			ui.C(signedPercent(c.Pct())), ui.C("×"+ui.Count(c.Copies)),
			ui.C(signedMoney(c.TotalDelta())))
	}
	return m.window(t.Lines(), paneCards, width)
}

// unpricedLines renders the holdings nothing can price.
func (m Model) unpricedLines(width int) []string {
	env := ui.Env{Width: width, Color: true, Clamp: true}
	t := ui.Table{
		Env: env, Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "HELD IN", Align: ui.Left, Priority: 5, Style: env.Dim()},
		},
	}
	for _, r := range m.unpriced {
		finish := r.Finish
		if finish == "normal" {
			finish = "-"
		}
		t.Add(ui.C(r.Name), ui.C(r.SetCode+"/"+r.CollectorNumber), ui.C(finish),
			ui.C("×"+ui.Count(r.Copies)), ui.C(r.HeldIn))
	}
	return m.window(t.Lines(), paneCards, width)
}

// viewHeader is the right pane's title and summary for the current mode.
func (m Model) viewHeader() (title, totals string) {
	switch m.view {
	case viewMovers:
		var net float64
		for _, c := range m.movers {
			net += c.TotalDelta()
		}
		since := m.now().Add(-moversWindow).Local().Format("2 Jan")
		return "MOVERS · SINCE " + since,
			fmt.Sprintf("%s moved · %s", ui.Count(len(m.movers)), signedMoney(net))
	case viewArbitrage:
		return m.arbitrageHeader()
	case viewUnpriced:
		var copies int
		for _, r := range m.unpriced {
			copies += r.Copies
		}
		return "UNPRICED", fmt.Sprintf("%s printings · %s copies",
			ui.Count(len(m.unpriced)), ui.Count(copies))
	}
	if sel := m.selectedContainer(); sel != nil {
		return "CARDS · " + strings.ToUpper(sel.Name),
			fmt.Sprintf("%s · %s", ui.Count(sel.Copies), ui.Money(sel.Value))
	}
	return "CARDS", ""
}

// signedMoney formats a movement, always carrying its sign. ui.Money already
// writes a minus; only the rise needs marking, so a column of them reads as
// direction rather than as a column of amounts. Matches the CLI's movers table.
func signedMoney(v float64) string {
	if v > 0 {
		return "+" + ui.Money(v)
	}
	return ui.Money(v)
}

// signedPercent formats a movement as a percentage, empty when the old price
// was zero and a percentage would be meaningless.
func signedPercent(frac float64) string {
	if frac == 0 {
		return ""
	}
	s := strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
	if frac > 0 {
		return "+" + s
	}
	return s
}
