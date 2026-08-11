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
// Every mode reads through the container pane: All Cards is the whole
// hoard, and any other selection narrows the view to what that container
// holds — the same contract the holdings view has always had. The header
// names the selection so a filtered table says so.
type viewMode int

const (
	viewHoldings viewMode = iota
	viewMovers
	viewWatches
	viewMarket
)

func (v viewMode) String() string {
	switch v {
	case viewMovers:
		return "movers"
	case viewWatches:
		// The unpriced holdings are the third table on this screen, not a
		// view of their own (owner's call) — so there is no "unpriced" here
		// for the view switcher to stop on.
		return "watches"
	case viewMarket:
		// Renamed on screen: the view holds three market questions —
		// arbitrage is just its first table, and a view and a table sharing
		// one name read as a stutter.
		return "market"
	}
	return "holdings"
}

// viewCycle is the v-key order (owner's call): the everyday reads first —
// cards, movers, market — then the alerts, which now carry the maintenance
// list as their third table. The enum's declaration order stays untouched;
// state arrays index by it.
var viewCycle = [...]viewMode{viewHoldings, viewMovers, viewMarket, viewWatches}

func (v viewMode) next() viewMode {
	for i, cur := range viewCycle {
		if cur == v {
			return viewCycle[(i+1)%len(viewCycle)]
		}
	}
	return viewHoldings
}

// moversWindowDays are the lookbacks the movers view cycles through with
// 'W', ascending with wrap (… → 7 → 30 → 90 → 7 → …); the default leads
// and matches `hoard movers`, so the two agree.
var moversWindowDays = []int{30, 90, 7}

// defaultPennyLimit is where the movers view's noise gate starts: cards
// priced at or under it hide by default (TogglePennyFilter shows them,
// SetPennyFilter moves the line).
const defaultPennyLimit = 0.50

// The penny filters' preference keys. Both views' gates persist: a floor
// moved during a session should still be the floor tomorrow.
const (
	setMoversPennies   = "movers.pennies"
	setMoversPennyLine = "movers.pennyLimit"
	setMarketPennies   = "market.pennies"
	setMarketFloor     = "market.floor"
)

// loadPennyFilters restores both views' gates. Absent or garbled values
// leave the defaults standing — a preference is never worth failing a
// launch over.
func (m *Model) loadPennyFilters() {
	s, err := m.store.Settings()
	if err != nil {
		return
	}
	if v, err := strconv.ParseBool(s[setMoversPennies]); err == nil {
		m.moversPennies = v
	}
	if n, err := strconv.ParseFloat(s[setMoversPennyLine], 64); err == nil && n >= 0 && n <= 100 {
		m.moversPennyLimit = n
	}
	if v, err := strconv.ParseBool(s[setMarketPennies]); err == nil {
		m.marketPennies = v
	}
	if n, err := strconv.ParseFloat(s[setMarketFloor], 64); err == nil && n >= 0 && n <= 100 {
		m.marketFloor = n
	}
}

// persistPennyFilters writes both views' gates. Called wherever either
// changes; a failed write surfaces but never undoes the change on screen.
func (m *Model) persistPennyFilters() {
	err := m.store.SaveSettings(map[string]string{
		setMoversPennies:   strconv.FormatBool(m.moversPennies),
		setMoversPennyLine: strconv.FormatFloat(m.moversPennyLimit, 'f', -1, 64),
		setMarketPennies:   strconv.FormatBool(m.marketPennies),
		setMarketFloor:     strconv.FormatFloat(m.marketFloor, 'f', -1, 64),
	})
	if err != nil {
		m.status, m.statusErr = "saving filter setting: "+err.Error(), true
	}
}

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
			m.persistPennyFilters()
			return nil
		},
	}
}

// promptSetMarketFloor is the market view's twin: same prompt, but the
// line it moves is the floor market.Collect filters on, so the commit
// re-collects from the day cache rather than re-deriving rows. Re-arms
// the gate the same way — moving the line answers with the line active.
func (m *Model) promptSetMarketFloor() {
	m.prompt = &prompt{
		label:    "hide market rows under",
		text:     strconv.FormatFloat(m.marketFloor, 'f', -1, 64),
		help:     "a dollar amount, like 1.00 (0 turns the gate off) · enter accept · esc cancel",
		validate: func(text string) error { _, err := parsePennyLimit(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			n, err := parsePennyLimit(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.marketFloor = n
			m.marketPennies = false
			m.status, m.statusErr = "penny filter < "+ui.Money(n)+" on", false
			// After the receipt: a day-cache miss replaces it with the
			// fresh-fetch ask, which is the truer answer.
			m.refreshMarketFloor()
			m.persistPennyFilters()
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

// moversCutoff is the instant the current window opens: the one the query asks
// from, the one the header names, and the one each row's FROM date is judged
// against. Three readings of the same moment had drifted apart into three
// expressions of it.
func (m Model) moversCutoff() time.Time { return m.now().Add(-m.moversWindow()) }

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
		since := m.moversCutoff().UTC().Format(time.RFC3339)
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
	case viewWatches:
		// One screen, two reads: the watches screen's third table is the
		// unpriced holdings, so both lists load together or neither does.
		watches, err := m.store.ListWatches()
		if err != nil {
			return fmt.Errorf("reading watches: %w", err)
		}
		unpriced, err := m.store.Unpriced()
		if err != nil {
			return fmt.Errorf("reading unpriced cards: %w", err)
		}
		m.allWatches, m.allUnpriced = watches, unpriced
	}
	m.deriveView()
	return nil
}

// deriveView applies the value floor, the container filter and — on the
// views that consume it — the typed query to the pristine slices, rebuilds
// the eligibility set on the views that grey containers out, and re-clamps
// the card cursor. Cheap and re-runnable: no database, no network — cycling
// the floor, moving the left cursor, or pressing a key into the filter bar
// all land here.
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
			if filtered {
				// Scope the count too: the row's Copies is hoard-wide, and a
				// deck-scoped QTY/IMPACT must describe the deck's copies, not
				// every copy across the collection (observed live).
				qty := m.containerQtyPriced(cid, c.ScryfallID, c.Finish)
				if qty == 0 {
					continue
				}
				c.Copies = qty
			}
			if bySet && c.SetCode != set {
				continue
			}
			// Last, so the query sees the container-scoped Copies above and
			// `qty` describes the same number the QTY column prints.
			if !m.filter.empty() && !m.filter.matches(moverAsCard(c), m.allowed) {
				continue
			}
			rows = append(rows, c)
		}
		m.filteredMovers = rows
		m.moversColW = measureMoverCols(rows, m.moversCutoff())
		m.applySort() // sorts filteredMovers and re-derives the page
	case viewWatches:
		watches := make([]store.WatchStatus, 0, len(m.allWatches))
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
			if !m.filter.empty() && !m.filter.matches(watchAsCard(w), m.allowed) {
				continue
			}
			watches = append(watches, w)
		}
		// The split is the screen: every watch lands in exactly one of the
		// two tables, by the same reading of Op that store.Met uses.
		m.overs, m.unders = nil, nil
		for _, w := range watches {
			if wantsUnder(w) {
				m.unders = append(m.unders, w)
				continue
			}
			m.overs = append(m.overs, w)
		}
		unpriced := make([]store.UnpricedRow, 0, len(m.allUnpriced))
		for _, r := range m.allUnpriced {
			if filtered {
				qty := m.containerQty(cid, r.ScryfallID, r.Finish)
				if qty == 0 {
					continue
				}
				r.Copies = qty
			}
			if bySet && r.SetCode != set {
				continue
			}
			// Last, so the query sees the container-scoped Copies above and
			// `qty` describes the number the QTY column prints.
			if !m.filter.empty() && !m.filter.matches(unpricedAsCard(r), m.allowed) {
				continue
			}
			unpriced = append(unpriced, r)
		}
		m.unpriced = unpriced
		// Eligibility is the union of the three tables: a container greyed
		// out here must hold nothing this whole screen would show, and the
		// unpriced holdings are as much a reason to keep it lit as a watch.
		// It reads the pristine rows, so the grey set holds still while the
		// floor and the query thin what is visible.
		if m.setsMode {
			m.viewEligible = m.eligibleSets(func(code string) bool {
				for _, w := range m.allWatches {
					if w.SetCode == code {
						return true
					}
				}
				for _, r := range m.allUnpriced {
					if r.SetCode == code {
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
				for _, r := range m.allUnpriced {
					if m.inContainer(id, r.ScryfallID, r.Finish) {
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
	// A cursor left on a table that no longer has rows moves to one that
	// does — an empty heading is somewhere to navigate to, not somewhere to
	// be put. Same rule the market view applies after re-ranking.
	if m.view == viewWatches && m.watchTotalRows() > 0 {
		if sec, _ := m.watchCursorPos(); m.watchCount(sec) == 0 {
			m.cursor[paneCards] = m.firstWatchCursor()
		}
	}
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
	case viewWatches:
		// Slots, not rows: the cursor also visits the heading of a table
		// with nothing under it — see watchRegion.
		return m.watchCursorSlots()
	case viewMarket:
		// Slots, not rows: the market view's cursor also visits the heading
		// of a table with nothing under it.
		return m.marketCursorSlots()
	}
	return len(m.cards)
}

// moversLines renders the risers and sinkers. Columns pin to the whole
// filtered ranking's measures (Width), so >/< holds the table's shape.
func (m Model) moversLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		w := m.moversColW
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10,
				Width: stableNameWidth(w.name, width)},
			{Title: "ID", Align: ui.Left, Priority: 7, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim(), Width: w.set},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim(), Width: w.fin},
			// Where each row's own measurement starts, on the rows that start
			// later than the window did; blank everywhere else, so a window the
			// history covers drops the column and the table keeps its shape.
			// It outlives WAS: a date taken away from beside the price it dates
			// would leave that price looking like the header's.
			{Title: "FROM", Align: ui.Right, Priority: 3, Style: env.Dim(), Width: w.from},
			{Title: "WAS", Align: ui.Right, Priority: 4, Style: env.Dim(), Width: w.was},
			{Title: "NOW", Align: ui.Right, Width: w.now},
			{Title: "CHANGE", Align: ui.Right, Priority: 3, Style: env.Dim(), Width: w.change},
			{Title: "QTY", Align: ui.Right, Priority: 2, Width: w.qty},
			{Title: "IMPACT", Align: ui.Right, Width: w.impact},
		}}
		// Each delta column fades on the diverging ramp against the whole
		// filtered ranking's extreme — not the page's — so a row keeps its
		// color as >/< leaf, and sorting by the column still reads as one
		// smooth green→gray→red sweep.
		pctMax, impactMax := store.MoverExtents(m.filteredMovers)
		cutoff := m.moversCutoff()
		for _, c := range m.movers {
			finish := ui.FinishTreated(c.Finish, c.Treatment)
			changeStyle := env.Diverge(ui.DivergeFrac(c.Pct(), pctMax))
			impactStyle := env.Diverge(ui.DivergeFrac(c.TotalDelta(), impactMax))
			t.Add(ui.Cell{Text: c.Name, Style: env.Identity(c.ColorIdentity)},
				ui.C(ui.Pips(c.ColorIdentity)),
				ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(c.BaselineFrom(cutoff)),
				ui.C(ui.Money(c.Old)), ui.C(ui.Money(c.New)),
				ui.Cell{Text: ui.SignedPercent(c.Pct()), Style: changeStyle}, ui.C(ui.Qty(c.Copies)),
				ui.Cell{Text: ui.SignedMoney(c.TotalDelta()), Style: impactStyle})
		}
		return t
	})
}

// viewHeader is the right pane's title and summary for the current mode.
func (m Model) viewHeader() (title, totals string) {
	switch m.view {
	case viewMovers:
		// Count and net speak for the whole filtered ranking, not the page
		// on screen; the page phrase beside them says where the reader is.
		var net float64
		for _, c := range m.filteredMovers {
			net += c.TotalDelta()
		}
		since := m.moversCutoff().Local().Format("2 Jan")
		return "MOVERS · SINCE " + since + m.viewScope(),
			fmt.Sprintf("%s moved · %s", ui.Count(len(m.filteredMovers)), ui.SignedMoney(net)) +
				m.tablePagePhrase(len(m.movers), m.moversPage, len(m.filteredMovers))
	case viewMarket:
		return m.marketHeader()
	case viewWatches:
		return m.watchHeader()
	}
	if sel := m.selectedContainer(); sel != nil {
		return "CARDS · " + strings.ToUpper(sel.Name),
			fmt.Sprintf("%s · %s", ui.Count(sel.Copies), ui.Money(sel.Value)) +
				m.tablePagePhrase(len(m.cards), m.cardsPage, len(m.filteredCards))
	}
	return "CARDS", ""
}
