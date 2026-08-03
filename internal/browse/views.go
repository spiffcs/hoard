package browse

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// viewMode is what the right pane is showing.
//
// Every mode reads through the container pane: All cards is the whole
// hoard, and any other selection narrows the view to what that container
// holds — the same contract the holdings view has always had. The header
// names the selection so a filtered table says so.
type viewMode int

const (
	viewHoldings viewMode = iota
	viewMovers
	viewUnpriced
	viewWatches
	viewMarket
)

func (v viewMode) String() string {
	switch v {
	case viewMovers:
		return "movers"
	case viewUnpriced:
		return "unpriced"
	case viewWatches:
		return "watches"
	case viewMarket:
		// Renamed on screen: the view holds three market questions —
		// arbitrage is just its first table, and a view and a table sharing
		// one name read as a stutter.
		return "market"
	}
	return "holdings"
}

func (v viewMode) next() viewMode { return (v + 1) % (viewMarket + 1) }

// moversWindowDays are the lookbacks the movers view cycles through with
// 'W', ascending with wrap (… → 7 → 30 → 90 → 7 → …); the default leads
// and matches `hoard movers`, so the two agree.
var moversWindowDays = []int{30, 90, 7}

// defaultPennyLimit is where the movers view's noise gate starts: cards
// priced at or under it hide by default (TogglePennyFilter shows them,
// SetPennyFilter moves the line).
const defaultPennyLimit = 0.20

// promptSetPennyLimit opens the penny filter's threshold prompt, prefilled
// with the current line. The commit re-arms the gate: asking to move the
// line while the pennies stayed showing would make the answer invisible.
func (m *Model) promptSetPennyLimit() {
	m.prompt = &prompt{
		label:    "hide movers at or under",
		text:     strconv.FormatFloat(m.moversPennyLimit, 'f', -1, 64),
		help:     "a dollar amount, like 0.20 (0 turns the gate off) · enter accept · esc cancel",
		validate: func(text string) error { _, err := parsePennyLimit(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			n, err := parsePennyLimit(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.moversPennyLimit = n
			m.moversPennies = false
			m.deriveView()
			m.status, m.statusErr = "penny filter ≤ "+ui.Money(n)+" on", false
			return nil
		},
	}
}

// parsePennyLimit reads the penny filter's line: a plain dollar amount,
// "$" optional. The cap keeps a typo from gating the whole view — a penny
// filter at $500 is a hidden movers list, not a noise gate.
func parsePennyLimit(text string) (float64, error) {
	t := strings.TrimPrefix(strings.TrimSpace(text), "$")
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || !(n >= 0 && n <= 100) {
		return 0, fmt.Errorf("say a dollar amount from 0 to 100, like 0.20")
	}
	return n, nil
}

// moversWindow is the current lookback.
func (m Model) moversWindow() time.Duration {
	days := moversWindowDays[0]
	if m.moversDaysIdx > 0 {
		days = moversWindowDays[m.moversDaysIdx%len(moversWindowDays)]
	}
	return time.Duration(days) * 24 * time.Hour
}

// loadView reads whichever analysis the right pane is showing, into the
// pristine slice — deriveView narrows it to what is visible.
//
// All are plain database reads and return in milliseconds, so they are loaded
// synchronously like the holdings. Nothing here touches the network.
func (m *Model) loadView() error {
	switch m.view {
	case viewMovers:
		// Serve the window from the session cache when nothing has
		// changed since it was filled — W cycles between three windows,
		// and each miss costs a double pass over the whole price history.
		if m.moversCacheGen == m.dataGen && m.moversCache != nil {
			if rows, ok := m.moversCache[m.moversDaysIdx]; ok {
				m.allMovers = rows
				break
			}
		}
		since := m.now().Add(-m.moversWindow()).UTC().Format(time.RFC3339)
		changes, err := m.store.Movers(since)
		if err != nil {
			return fmt.Errorf("reading movers: %w", err)
		}
		m.allMovers = store.MoversByImpact(changes)
		if m.moversCacheGen != m.dataGen || m.moversCache == nil {
			m.moversCache = map[int][]store.PriceChange{}
			m.moversCacheGen = m.dataGen
		}
		m.moversCache[m.moversDaysIdx] = m.allMovers
	case viewUnpriced:
		rows, err := m.store.Unpriced()
		if err != nil {
			return fmt.Errorf("reading unpriced cards: %w", err)
		}
		m.allUnpriced = rows
	case viewWatches:
		rows, err := m.store.ListWatches()
		if err != nil {
			return fmt.Errorf("reading watches: %w", err)
		}
		m.allWatches = rows
	}
	m.deriveView()
	return nil
}

// deriveView applies the value floor and the container filter to the
// pristine slices, rebuilds the eligibility set on the views that grey
// containers out, and re-clamps the card cursor. Cheap and re-runnable: no
// database, no network — cycling the floor or moving the left cursor lands
// here.
//
// Eligibility is computed from the pristine (floor-ignored) rows so the
// grey set holds still while M cycles; the floor only thins visible rows.
func (m *Model) deriveView() {
	m.viewEligible = nil
	cid, filtered := m.filterContainerID()
	// The set scope is the container scope's sibling — mutually exclusive
	// with it, and an exact code match: both sides come from cards.set_code,
	// so no finish folding applies.
	set, bySet := m.filterSetCode()
	switch m.view {
	case viewMovers:
		rows := make([]store.PriceChange, 0, len(m.allMovers))
		for _, c := range m.allMovers {
			if m.underFloor(&c.New) {
				continue
			}
			// The penny gate, separate from the floor: a $0.15 card
			// twitching by a cent is a row, not information.
			if !m.moversPennies && c.New <= m.moversPennyLimit {
				continue
			}
			if filtered && !m.inContainerPriced(cid, c.ScryfallID, c.Finish) {
				continue
			}
			if bySet && c.SetCode != set {
				continue
			}
			rows = append(rows, c)
		}
		m.movers = rows
		m.applySort()
	case viewUnpriced:
		rows := make([]store.UnpricedRow, 0, len(m.allUnpriced))
		for _, r := range m.allUnpriced {
			if filtered && !m.inContainer(cid, r.ScryfallID, r.Finish) {
				continue
			}
			if bySet && r.SetCode != set {
				continue
			}
			rows = append(rows, r)
		}
		m.unpriced = rows
		if m.setsMode {
			m.viewEligible = m.eligibleSets(func(code string) bool {
				for _, r := range m.allUnpriced {
					if r.SetCode == code {
						return true
					}
				}
				return false
			})
		} else {
			m.viewEligible = m.eligibleContainers(func(id int64) bool {
				for _, r := range m.allUnpriced {
					if m.inContainer(id, r.ScryfallID, r.Finish) {
						return true
					}
				}
				return false
			})
		}
		m.applySort()
	case viewWatches:
		rows := make([]store.WatchStatus, 0, len(m.allWatches))
		for _, w := range m.allWatches {
			if m.underFloor(w.PriceUSD) {
				continue
			}
			if filtered && !m.inContainerPriced(cid, w.ScryfallID, w.Finish) {
				continue
			}
			if bySet && w.SetCode != set {
				continue
			}
			rows = append(rows, w)
		}
		m.watches = rows
		if m.setsMode {
			m.viewEligible = m.eligibleSets(func(code string) bool {
				for _, w := range m.allWatches {
					if w.SetCode == code {
						return true
					}
				}
				return false
			})
		} else {
			m.viewEligible = m.eligibleContainers(func(id int64) bool {
				for _, w := range m.allWatches {
					if m.inContainerPriced(id, w.ScryfallID, w.Finish) {
						return true
					}
				}
				return false
			})
		}
		m.applySort()
	case viewMarket:
		m.applyMarketRows()
	default:
		return
	}
	m.clampCursor(paneCards)
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
	case viewWatches:
		return len(m.watches)
	case viewMarket:
		return m.marketTotalRows()
	}
	return len(m.cards)
}

// moversLines renders the risers and sinkers.
func (m Model) moversLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "ID", Align: ui.Left, Priority: 7, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "IMPACT", Align: ui.Right},
		}}
		// Each delta column fades on the diverging ramp against its own
		// visible extreme, so sorting by the column reads as one smooth
		// green→gray→red sweep.
		pctMax, impactMax := store.MoverExtents(m.movers)
		for _, c := range m.movers {
			finish := ui.Finish(c.Finish)
			changeStyle := env.Diverge(ui.DivergeFrac(c.Pct(), pctMax))
			impactStyle := env.Diverge(ui.DivergeFrac(c.TotalDelta(), impactMax))
			t.Add(ui.Cell{Text: c.Name, Style: env.Identity(c.ColorIdentity)},
				ui.C(ui.Pips(c.ColorIdentity)),
				ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(ui.Money(c.Old)), ui.C(ui.Money(c.New)),
				ui.Cell{Text: ui.SignedPercent(c.Pct()), Style: changeStyle}, ui.C(ui.Qty(c.Copies)),
				ui.Cell{Text: ui.SignedMoney(c.TotalDelta()), Style: impactStyle})
		}
		return t
	})
}

// unpricedLines renders the holdings nothing can price.
func (m Model) unpricedLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "ID", Align: ui.Left, Priority: 6, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "HELD IN", Align: ui.Left, Priority: 5, Style: env.Dim()},
		}}
		for _, r := range m.unpriced {
			finish := ui.Finish(r.Finish)
			t.Add(ui.Cell{Text: r.Name, Style: env.Identity(r.ColorIdentity)},
				ui.C(ui.Pips(r.ColorIdentity)),
				ui.C(ui.Printing(r.SetCode, r.CollectorNumber)), ui.C(finish),
				ui.C(ui.Qty(r.Copies)), ui.C(r.HeldIn))
		}
		return t
	})
}

// viewHeader is the right pane's title and summary for the current mode.
func (m Model) viewHeader() (title, totals string) {
	switch m.view {
	case viewMovers:
		var net float64
		for _, c := range m.movers {
			net += c.TotalDelta()
		}
		since := m.now().Add(-m.moversWindow()).Local().Format("2 Jan")
		return "MOVERS · SINCE " + since + m.viewScope(),
			fmt.Sprintf("%s moved · %s", ui.Count(len(m.movers)), ui.SignedMoney(net))
	case viewMarket:
		return m.marketHeader()
	case viewUnpriced:
		var copies int
		for _, r := range m.unpriced {
			copies += r.Copies
		}
		return "UNPRICED" + m.viewScope(), fmt.Sprintf("%s printings · %s copies",
			ui.Count(len(m.unpriced)), ui.Count(copies))
	case viewWatches:
		met := 0
		for _, w := range m.watches {
			if w.Met() {
				met++
			}
		}
		return "WATCHES" + m.viewScope(), fmt.Sprintf("%s watches · %s met",
			ui.Count(len(m.watches)), ui.Count(met))
	}
	if sel := m.selectedContainer(); sel != nil {
		return "CARDS · " + strings.ToUpper(sel.Name),
			fmt.Sprintf("%s · %s", ui.Count(sel.Copies), ui.Money(sel.Value))
	}
	return "CARDS", ""
}
