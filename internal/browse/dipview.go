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
		return "climbing every check, never once down"
	}
	return "at the floor of the window, well off the high"
}

func (m Model) trendOptions() store.TrendOptions {
	settled := m.now().AddDate(0, 0, -store.SettlingDays()).Format("2006-01-02")
	return store.TrendOptions{
		Since:         m.moversCutoff().UTC().Format(time.RFC3339),
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

type dipSectionInfo struct {
	count    int
	curStart int
	span     int
}

func (m Model) dipSectionsInfo() [2]dipSectionInfo {
	dips, mom := len(m.dips), len(m.momentum)
	return [2]dipSectionInfo{
		{count: dips, curStart: 0, span: max(dips, 1)},
		{count: mom, curStart: max(dips, 1), span: max(mom, 1)},
	}
}

func (m Model) dipCursorSlots() int {
	var n int
	for _, s := range m.dipSectionsInfo() {
		n += s.span
	}
	return n
}

func (m Model) firstDipCursor() int {
	for _, s := range m.dipSectionsInfo() {
		if s.count > 0 {
			return s.curStart
		}
	}
	return 0
}

func (m Model) dipSectionBudgets() [2]int {
	secs := m.dipSectionsInfo()
	counts := make([]int, dipSectionCount)
	for i := range counts {
		counts[i] = secs[i].count
	}
	pool := max(m.visibleRows()-(2+dipSectionCount*2), 0)
	sec, _ := m.dipCursorPos()
	var budget [2]int
	copy(budget[:], sectionBudgets(counts, pool, sec))
	return budget
}

func (m *Model) deriveDipPages() {
	totals := m.dipSectionTotals()
	for i, tot := range totals {
		maxPage := 0
		if tot > 0 {
			maxPage = (tot - 1) / pageSize
		}
		m.dipPage[i] = min(max(m.dipPage[i], 0), maxPage)
	}
	slice := func(rows []store.TrendRow, pg int) []store.TrendRow {
		lo := min(pg*pageSize, len(rows))
		return rows[lo:min(lo+pageSize, len(rows))]
	}
	m.dips = slice(m.filteredDips, m.dipPage[secDip])
	m.momentum = slice(m.filteredMomentum, m.dipPage[secMomentum])
}

func (m *Model) turnDipPage(dir int) {
	sec, _ := m.dipCursorPos()
	tot := m.dipSectionTotals()[sec]
	maxPage := 0
	if tot > 0 {
		maxPage = (tot - 1) / pageSize
	}
	next := min(max(m.dipPage[sec]+dir, 0), maxPage)
	if next == m.dipPage[sec] {
		if maxPage == 0 {
			m.status, m.statusErr = "one page here", false
		} else if dir > 0 {
			m.status, m.statusErr = "last page", false
		} else {
			m.status, m.statusErr = "first page", false
		}
		return
	}
	m.dipPage[sec] = next
	m.deriveDipPages()
	m.dipSecOffset[sec] = 0
	m.cursor[paneCards] = m.dipSectionsInfo()[sec].curStart
	m.scrollIntoView()
	m.status, m.statusErr = fmt.Sprintf("%s · page %d/%d · rows %d–%d of %d",
		dipSection(sec).title(), next+1, maxPage+1, next*pageSize+1,
		min((next+1)*pageSize, tot), tot), false
}

func (m *Model) jumpDipSection(dir int) {
	secs := m.dipSectionsInfo()
	cur, _ := m.dipCursorPos()
	for i := cur + dir; i >= 0 && i < len(secs); i += dir {
		m.cursor[paneCards] = secs[i].curStart
		m.focus = paneCards
		m.scrollIntoView()
		return
	}
}

func (m *Model) scrollDipIntoView() {
	secs := m.dipSectionsInfo()
	budgets := m.dipSectionBudgets()
	if len(m.dips)+len(m.momentum) > 0 {
		sec, idx := m.dipCursorPos()
		if b := budgets[sec]; b > 0 {
			if idx < m.dipSecOffset[sec] {
				m.dipSecOffset[sec] = idx
			}
			if idx >= m.dipSecOffset[sec]+b {
				m.dipSecOffset[sec] = idx - b + 1
			}
		}
	}
	m.dipSecOffset[secDip] = clampOffset(m.dipSecOffset[secDip],
		secs[secDip].count, budgets[secDip])
	m.dipSecOffset[secMomentum] = clampOffset(m.dipSecOffset[secMomentum],
		secs[secMomentum].count, budgets[secMomentum])
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
	counts := m.dipSectionTotals()
	return "DIP & MOMENTUM · SINCE " + since + m.viewScope(),
		fmt.Sprintf("%s at the floor · %s climbing",
			ui.Count(counts[secDip]), ui.Count(counts[secMomentum]))
}

func (m Model) dipCursorPos() (sec, idx int) {
	cur := min(max(m.cursor[paneCards], 0), max(m.dipCursorSlots()-1, 0))
	secs := m.dipSectionsInfo()
	for i := len(secs) - 1; i >= 0; i-- {
		if cur >= secs[i].curStart {
			return i, min(cur-secs[i].curStart, max(secs[i].count-1, 0))
		}
	}
	return 0, 0
}

func clampOffset(off, count, budget int) int {
	return min(max(off, 0), max(count-budget, 0))
}

func (m Model) selectedTrendRow() *store.TrendRow {
	sec, idx := m.dipCursorPos()
	rows := m.dipRows(dipSection(sec))
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	return &rows[idx]
}
