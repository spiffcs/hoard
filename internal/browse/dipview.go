package browse

import (
	"fmt"
	"time"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type dipSection int

const (
	secDip dipSection = iota
	secMomentum
)

const dipSectionCount = 2

func (s dipSection) title() string {
	if s == secMomentum {
		return "MOMENTUM"
	}
	return "DIP"
}

var (
	dipSortColumns      = []string{"off high", "name", "set/num", "finish", "high", "low", "now"}
	momentumSortColumns = []string{"streak", "name", "set/num", "finish", "was", "now", "change"}
)

func (s dipSection) sortColumns() []string {
	if s == secMomentum {
		return momentumSortColumns
	}
	return dipSortColumns
}

func (s dipSection) note() string {
	if s == secMomentum {
		return "climbing every check"
	}
	return "at the floor of the window"
}

func (m Model) trendOptions() store.TrendOptions {
	settled := m.now().AddDate(0, 0, -store.SettlingDays()).Format("2006-01-02")
	return store.TrendOptions{
		Since:         m.moversCutoff().UTC().Truncate(24 * time.Hour).Format(time.RFC3339),
		SettledBefore: settled,
		MinPrice:      m.floorMin(),
		MinChecks:     4,
	}
}

func (m Model) dipRows(s dipSection) []store.TrendRow {
	if s == secMomentum {
		return m.momentum
	}
	return m.dips
}

func (m Model) dipSectionTotals() [2]int {
	return [2]int{len(m.filteredDips), len(m.filteredMomentum)}
}

func (m Model) dipSectionsInfo() sectionList {
	return newSectionList(len(m.dips), len(m.momentum))
}

func (m Model) dipCursorSlots() int { return m.dipSectionsInfo().cursorSlots() }

func (m Model) firstDipCursor() int { return m.dipSectionsInfo().firstCursor() }

func (m Model) dipSectionBudgets() [2]int {
	pool := max(m.visibleRows()-(2+dipSectionCount*2), 0)
	sec, _ := m.dipCursorPos()
	var budget [2]int
	copy(budget[:], m.dipSectionsInfo().budgets(pool, sec))
	return budget
}

func (m *Model) deriveDipPages() {
	totals := m.dipSectionTotals()
	for i, tot := range totals {
		m.dipPage[i] = pager{page: m.dipPage[i], size: pageSize, total: tot}.clamped()
	}
	slice := func(rows []store.TrendRow, pg int) []store.TrendRow {
		lo, hi := pager{page: pg, size: pageSize, total: len(rows)}.bounds()
		return rows[lo:hi]
	}
	m.dips = slice(m.filteredDips, m.dipPage[secDip])
	m.momentum = slice(m.filteredMomentum, m.dipPage[secMomentum])
}

func (m *Model) turnDipPage(dir int) {
	sec, _ := m.dipCursorPos()
	pg := pager{page: m.dipPage[sec], size: pageSize, total: m.dipSectionTotals()[sec]}
	next, edge := pg.turn(dir)
	if edge != "" {
		m.status, m.statusErr = edge, false
		return
	}
	m.dipPage[sec] = next
	m.deriveDipPages()
	m.dipSecOffset[sec] = 0
	m.cursor[paneCards] = m.dipSectionsInfo()[sec].curStart
	m.scrollIntoView()

	pg.page = next
	m.status, m.statusErr = dipSection(sec).title()+" · "+pg.rangePhrase(), false
}

func (m *Model) jumpDipSection(dir int) {
	if next, ok := m.dipSectionsInfo().jump(m.cursor[paneCards], dir); ok {
		m.cursor[paneCards] = next
		m.focus = paneCards
		m.scrollIntoView()
	}
}

func (m *Model) scrollDipIntoView() {
	budgets := m.dipSectionBudgets()
	m.dipSectionsInfo().scrollIntoView(m.dipSecOffset[:], budgets[:], m.cursor[paneCards])
}

func (m Model) dipEmptyNote(s dipSection) string {
	if !m.filter.Empty() {
		return "nothing matches the filter"
	}
	if s == secMomentum {
		return "no printing climbed every check in this window"
	}
	return "nothing is sitting at its floor in this window"
}

func (m Model) dipLines(width int) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	budgets := m.dipSectionBudgets()
	totals := m.dipSectionTotals()
	cursorSec, cursorIdx := m.dipCursorPos()
	hasCursor := m.focus == paneCards

	var out []string
	for i := range dipSectionCount {
		sec := dipSection(i)
		if i > 0 {
			out = append(out, "")
		}
		rows := m.dipRows(sec)
		head := m.theme.Title.Render(sec.title()) + "  " + m.theme.Help.Render(sec.note())
		if frag := pagePhrase(m.dipSecOffset[i], budgets[i], len(rows),
			m.dipPage[i], totals[i], pageSize); frag != "" {
			head += m.theme.Help.Render(frag)
		}
		if len(rows) == 0 {
			if hasCursor && i == cursorSec {
				head = ui.Restyle(fit(head, width), m.theme.Cursor)
			}
			out = append(out, head, m.theme.Help.Render(m.dipEmptyNote(sec)))
			continue
		}
		out = append(out, head)

		t := dipSectionTable(env, sec, rows, m.dipChangeExtent())
		t.Env, t.Header = env, true
		lines := t.Lines()
		out = append(out, lines[0])

		off := m.dipSecOffset[i]
		for k := off; k < min(off+budgets[i], len(rows)); k++ {
			line := lines[1+k]
			if hasCursor && i == cursorSec && k == cursorIdx {
				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

func (m Model) dipChangeExtent() float64 {
	return store.TrendExtent(m.filteredMomentum)
}

func dipSectionTable(env ui.Env, sec dipSection, rows []store.TrendRow,
	extent float64) ui.Table {
	if sec == secMomentum {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "STREAK", Align: ui.Right},
			{Title: "WAS", Align: ui.Right, Priority: 3},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right},
		}}
		for _, r := range rows {
			t.Add(ui.C(r.Name), ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
				ui.C(ui.FinishTreated(r.Finish, r.Treatment)),
				ui.Cell{Text: fmt.Sprintf("%d up", r.Ups),
					Style: env.Grade(market.StreakGrade(r.Ups))},
				ui.C(ui.Money(r.First)), ui.C(ui.Money(r.Last)),
				ui.Cell{Text: ui.SignedPercent(r.Change()),
					Style: env.Diverge(ui.DivergeFrac(r.Change(), extent))})
		}
		return t
	}

	t := ui.Table{Cols: []ui.Col{
		{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
		{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
		{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
		{Title: "HIGH", Align: ui.Right, Priority: 3},
		{Title: "LOW", Align: ui.Right, Priority: 2, Style: env.Dim()},
		{Title: "NOW", Align: ui.Right},
		{Title: "OFF HIGH", Align: ui.Right},
	}}
	for _, r := range rows {
		t.Add(ui.C(r.Name), ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
			ui.C(ui.FinishTreated(r.Finish, r.Treatment)),
			ui.C(ui.Money(r.High)), ui.C(ui.Money(r.Low)), ui.C(ui.Money(r.Last)),
			ui.Cell{Text: ui.SignedPercent(r.OffHigh()),
				Style: env.Grade(market.DipGrade(-r.OffHigh()))})
	}
	return t
}

func (m Model) dipHeader() (title, totals string) {
	since := m.moversCutoff().Local().Format("2 Jan")
	title = "DIP & MOMENTUM · SINCE " + since + m.viewScope()
	if m.trendBusy() {
		return title, m.spinner.View() + " reading the last " + fmt.Sprint(m.moversStop()) + " days…"
	}
	counts := m.dipSectionTotals()
	return title, fmt.Sprintf("%s at the floor · %s climbing",
		ui.Count(counts[secDip]), ui.Count(counts[secMomentum]))
}

func (m Model) dipCursorPos() (sec, idx int) {
	return m.dipSectionsInfo().cursorPos(m.cursor[paneCards])
}

func (m Model) selectedTrendRow() *store.TrendRow {
	sec, idx := m.dipCursorPos()
	rows := m.dipRows(dipSection(sec))
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	return &rows[idx]
}
