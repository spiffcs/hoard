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

type viewMode int

const (
	viewHoldings viewMode = iota
	viewMovers
	viewWatches
	viewMarket
	viewDip
)

func (v viewMode) String() string {
	switch v {
	case viewMovers:
		return "movers"
	case viewWatches:

		return "watches"
	case viewMarket:

		return "market"
	case viewDip:

		return "dip"
	}
	return "holdings"
}

var viewCycle = [...]viewMode{viewHoldings, viewMovers, viewMarket, viewDip, viewWatches}

func (v viewMode) next() viewMode {
	for i, cur := range viewCycle {
		if cur == v {
			return viewCycle[(i+1)%len(viewCycle)]
		}
	}
	return viewHoldings
}

var moversWindowDays = []int{30, 90, 7}

const moversCostBasis = -1

func (m Model) moversStops() []int {
	if !m.hasCostBasis {
		return moversWindowDays
	}
	return append(append([]int(nil), moversWindowDays...), moversCostBasis)
}

func (m Model) moversStop() int {
	stops := m.moversStops()
	return stops[m.moversDaysIdx%len(stops)]
}

func (m Model) onCostBasis() bool { return m.moversStop() == moversCostBasis }

func (m Model) moversLookbackHelp() string {
	if m.hasCostBasis {
		return "lookback 7/30/90 days or cost basis"
	}
	return "lookback 7/30/90 days"
}

const defaultPennyLimit = 1.00

const oldMoversPennyLimit = 0.50

const (
        setShowPennies      = "pennies.show"
        setPennyLimit       = "pennies.limit"
	setMoversPennies    = "movers.pennies"
	setMoversPennyLine  = "movers.pennyLimit"
	setSidebarCollapsed = "sidebar.collapsed"
)

func settingFloat(s map[string]string, key string) (float64, bool) {
        n, err := strconv.ParseFloat(s[key], 64)
        if err != nil || n < 0 || n > 100 {
                return 0, false
        }
        return n, true
}

func (m *Model) loadPennyFilters() {
	s, err := m.store.Settings()
	if err != nil {
		return
	}
        if n, ok := settingFloat(s, setPennyLimit); ok {
                m.pennyLimit = n
        } else if n, ok := settingFloat(s, setMoversPennyLine); ok && n != oldMoversPennyLimit {
                m.pennyLimit = n
	}
        for _, key := range []string{setShowPennies, setMoversPennies} {
                if v, err := strconv.ParseBool(s[key]); err == nil {
                        m.showPennies = v
                        break
                }
	}
}

func (m *Model) persistPennyFilters() {
	err := m.store.SaveSettings(map[string]string{
                setShowPennies: strconv.FormatBool(m.showPennies),
                setPennyLimit:  strconv.FormatFloat(m.pennyLimit, 'f', -1, 64),
	})
	if err != nil {
		m.status, m.statusErr = "saving filter setting: "+err.Error(), true
	}
}

func (m Model) pennyPhrase() string {
        return "penny filter ≤ " + ui.Money(m.pennyLimit)
}

func (m Model) pennyGated(price float64) bool {
        return !m.showPennies && price <= m.pennyLimit
}

func (m *Model) applyPennyFilter() {
        m.deriveView()
        m.refreshMarketFloor()
        m.persistPennyFilters()
}

func (m *Model) togglePennyFilter() {
        m.showPennies = !m.showPennies
        state := "on"
        if m.showPennies {
                state = "off"
	}
        m.status, m.statusErr = m.pennyPhrase()+" "+state, false
        m.applyPennyFilter()
}

func (m *Model) promptSetPennyLimit() {
	m.prompt = &prompt{
                label:    "hide rows at or under",
                text:     strconv.FormatFloat(m.pennyLimit, 'f', -1, 64),
                help:     "a dollar amount, like 0.20 (0 turns the gate off) · enter accept · esc cancel",
		validate: func(text string) error { _, err := parsePennyLimit(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			n, err := parsePennyLimit(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
                        m.pennyLimit = n
                        m.showPennies = false
                        m.status, m.statusErr = m.pennyPhrase()+" on", false
                        m.applyPennyFilter()
			return nil
		},
	}
}

func parsePennyLimit(text string) (float64, error) {
	t := strings.TrimPrefix(strings.TrimSpace(text), "$")
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || !(n >= 0 && n <= 100) {
		return 0, fmt.Errorf("say a dollar amount from 0 to 100, like 0.20")
	}
	return n, nil
}

func (m Model) moversWindow() time.Duration {
	days := m.moversStop()
	if days == moversCostBasis {
		days = moversWindowDays[0]
	}
	return time.Duration(days) * 24 * time.Hour
}

func (m Model) moversCutoff() time.Time { return m.now().Add(-m.moversWindow()) }

func (m *Model) loadView() error {
	switch m.view {
	case viewMovers:

		if m.moversCacheGen == m.dataGen && m.moversCache != nil {
			if rows, ok := m.moversCache[m.moversDaysIdx]; ok {
				m.allMovers = rows
				break
			}
		}
		var changes []store.PriceChange
		var err error
		if m.onCostBasis() {
			changes, err = m.store.CostBasisMovers()
			if err != nil {
				return fmt.Errorf("reading cost basis: %w", err)
			}
		} else {
			since := m.moversCutoff().UTC().Format(time.RFC3339)
			changes, err = m.store.Movers(since)
			if err != nil {
				return fmt.Errorf("reading movers: %w", err)
			}
		}
		m.allMovers = store.MoversByImpact(changes)
		if m.moversCacheGen != m.dataGen || m.moversCache == nil {
			m.moversCache = map[int][]store.PriceChange{}
			m.moversCacheGen = m.dataGen
		}
		m.moversCache[m.moversDaysIdx] = m.allMovers
	case viewWatches:

		watches, err := m.store.ListWatches()
		if err != nil {
			return fmt.Errorf("reading watches: %w", err)
		}
		unpriced, err := m.store.Unpriced()
		if err != nil {
			return fmt.Errorf("reading unpriced cards: %w", err)
		}
		m.allWatches, m.allUnpriced = watches, unpriced
	case viewDip:

		o := m.trendOptions()
		dips, err := m.store.Dips(o)
		if err != nil {
			return fmt.Errorf("reading dips: %w", err)
		}
		momentum, err := m.store.Momentum(o)
		if err != nil {
			return fmt.Errorf("reading momentum: %w", err)
		}
		m.allDips, m.allMomentum = dips, momentum
	}
	m.deriveView()
	return nil
}

func (m *Model) filterTrends(rows []store.TrendRow) []store.TrendRow {
	cid, filtered := m.filterContainerID()
	set, bySet := m.filterSetCode()

	out := make([]store.TrendRow, 0, len(rows))
	for _, r := range rows {
		last := r.Last
                if m.underFloor(&last) || m.pennyGated(last) {
			continue
		}
		if filtered {
			qty := m.containerQtyPriced(cid, r.ScryfallID, r.Finish)
			if qty == 0 {
				continue
			}
			r.Copies = qty
		}
		if bySet && r.SetCode != set {
			continue
		}
		if !m.filter.Empty() && !m.filter.Matches(trendAsCard(r).subject(), m.allowed) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (m *Model) deriveView() {
	m.viewEligible = nil
	cid, filtered := m.filterContainerID()

	set, bySet := m.filterSetCode()
	switch m.view {
	case viewMovers:
		rows := make([]store.PriceChange, 0, len(m.allMovers))
		for _, c := range m.allMovers {
			if m.underFloor(&c.New) {
				continue
			}

                        if m.pennyGated(c.New) {
				continue
			}
			if filtered {

				qty := m.containerQtyPriced(cid, c.ScryfallID, c.Finish)
				if qty == 0 {
					continue
				}
				c.Copies = qty
			}
			if bySet && c.SetCode != set {
				continue
			}

			if !m.filter.Empty() && !m.filter.Matches(moverAsCard(c).subject(), m.allowed) {
				continue
			}
			rows = append(rows, c)
		}
		m.filteredMovers = rows
		m.moversColW = measureMoverCols(rows, m.moversCutoff())
		m.applySort()
	case viewDip:
		m.filteredDips = m.filterTrends(m.allDips)
		m.filteredMomentum = m.filterTrends(m.allMomentum)
		m.applySort()
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
			if !m.filter.Empty() && !m.filter.Matches(watchAsCard(w).subject(), m.allowed) {
				continue
			}
			watches = append(watches, w)
		}

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

			if !m.filter.Empty() && !m.filter.Matches(unpricedAsCard(r).subject(), m.allowed) {
				continue
			}
			unpriced = append(unpriced, r)
		}
		m.unpriced = unpriced

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

	if m.view == viewWatches && m.watchTotalRows() > 0 {
		if sec, _ := m.watchCursorPos(); m.watchCount(sec) == 0 {
			m.cursor[paneCards] = m.firstWatchCursor()
		}
	}
}

func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m Model) viewRowCount() int {
	switch m.view {
	case viewMovers:
		return len(m.movers)
	case viewWatches:

		return m.watchCursorSlots()
	case viewMarket:

		return m.marketCursorSlots()
	case viewDip:

		return m.dipCursorSlots()
	}
	return len(m.cards)
}

func (m Model) moversLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		w := m.moversColW
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10,
				Width: stableNameWidth(w.name, width)},
			{Title: "ID", Align: ui.Left, Priority: 7, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim(), Width: w.set},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim(), Width: w.fin},

			{Title: "FROM", Align: ui.Right, Priority: 3, Style: env.Dim(), Width: w.from},
			{Title: "WAS", Align: ui.Right, Priority: 4, Style: env.Dim(), Width: w.was},
			{Title: "NOW", Align: ui.Right, Width: w.now},
			{Title: "CHANGE", Align: ui.Right, Priority: 3, Style: env.Dim(), Width: w.change},
			{Title: "QTY", Align: ui.Right, Priority: 2, Width: w.qty},
			{Title: "IMPACT", Align: ui.Right, Width: w.impact},
		}}

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

func (m Model) viewHeader() (title, totals string) {
	switch m.view {
	case viewMovers:

		now := m.now()
		var net float64
		var heldOut int
		if sel := m.selectedContainer(); sel != nil && sel.settling(now) {
			for _, c := range m.filteredMovers {
				net += c.TotalDelta()
			}
		} else {
			net, heldOut = store.NetMoved(m.filteredMovers, now)
		}

		env := ui.Env{Color: m.env.Color}
		sign := 0.0
		if net > 0 {
			sign = 1
		} else if net < 0 {
			sign = -1
		}
		summary := fmt.Sprintf("%s moved · %s",
			ui.Count(len(m.filteredMovers)), env.Diverge(sign)(ui.SignedMoney(net)))
		if heldOut > 0 {
			summary += " · " + ui.PluralCount(heldOut,
				"settling set held out", "settling sets held out")
		}
		heading := "MOVERS · SINCE " + m.moversCutoff().Local().Format("2 Jan")
		if m.onCostBasis() {
			heading = "MOVERS · AGAINST WHAT YOU PAID"
		}
		return heading + m.viewScope(),
			joinPhrases(m.tablePagePhrase(len(m.movers), m.moversPage, len(m.filteredMovers)), summary)
	case viewMarket:
		return m.marketHeader()
	case viewDip:
		return m.dipHeader()
	case viewWatches:
		return m.watchHeader()
	}
	if sel := m.selectedContainer(); sel != nil {
		if picked := m.selectionSummary(); picked != "" {
			return "CARDS · " + strings.ToUpper(sel.Name), picked
		}
		totals := fmt.Sprintf("%s · %s", ui.Count(sel.Copies), ui.Money(sel.Value))
		if sel.Kind == kindSet {
			if m.setUnowned {
				totals = ui.Money(m.setMissingCost) + " to finish"
			}
			totals = m.setTally() + " · " + totals
		}
		return "CARDS · " + strings.ToUpper(sel.Name),
			totals + m.tablePagePhrase(len(m.cards), m.cardsPage, len(m.filteredCards))
	}
	return "CARDS", ""
}

func joinPhrases(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimPrefix(p, " · "); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}
