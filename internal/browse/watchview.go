package browse

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type watchSection int

const (
	secOvers watchSection = iota
	secUnders
	secUnpriced

	watchSectionCount
)

func (s watchSection) title() string {
	switch s {
	case secUnders:
		return "UNDERS"
	case secUnpriced:
		return "UNPRICED"
	}
	return "OVERS"
}

func (s watchSection) note() string {
	switch s {
	case secUnders:
		return "waiting for a price to fall"
	case secUnpriced:
		return "held, and nothing can price them"
	}
	return "waiting for a price to rise"
}

func (s watchSection) emptyNote() string {
	switch s {
	case secUnders:
		return "no watch is waiting for a drop"
	case secUnpriced:
		return "every card you own has a price"
	}
	return "no watch is waiting for a rise"
}

func (s watchSection) sortColumns() []string {
	if s == secUnpriced {
		return unpricedSortColumns
	}
	return watchSortColumns
}

var (
	watchSortColumns    = []string{"state", "name", "threshold", "price"}
	unpricedSortColumns = []string{"name", "set/num", "finish", "qty", "held in"}
)

func wantsUnder(w store.WatchStatus) bool { return w.Op == "under" }

type watchRegion struct {
	sec      watchSection
	count    int
	curStart int
	span     int
}

func (m Model) watchCount(s watchSection) int {
	switch s {
	case secOvers:
		return len(m.overs)
	case secUnders:
		return len(m.unders)
	case secUnpriced:
		return len(m.unpriced)
	}
	return 0
}

func (m Model) watchRegions() []watchRegion {
	out := make([]watchRegion, 0, watchSectionCount)
	cur := 0
	for s := range watchSectionCount {
		r := watchRegion{sec: s, count: m.watchCount(s), curStart: cur}
		r.span = max(r.count, 1)
		out = append(out, r)
		cur += r.span
	}
	return out
}

func (m Model) watchTotalRows() int {
	return len(m.overs) + len(m.unders) + len(m.unpriced)
}

func (m Model) watchCursorSlots() int {
	n := 0
	for _, r := range m.watchRegions() {
		n += r.span
	}
	return n
}

func (m Model) watchCursorPos() (watchSection, int) {
	regions := m.watchRegions()
	cur := min(max(m.cursor[paneCards], 0), max(m.watchCursorSlots()-1, 0))
	for i := len(regions) - 1; i >= 0; i-- {
		if cur >= regions[i].curStart {
			return regions[i].sec, min(cur-regions[i].curStart, max(regions[i].count-1, 0))
		}
	}
	return secOvers, 0
}

func (m Model) firstWatchCursor() int {
	for _, r := range m.watchRegions() {
		if r.count > 0 {
			return r.curStart
		}
	}
	return 0
}

func (m Model) selectedWatch() *store.WatchStatus {
	if m.view != viewWatches {
		return nil
	}
	sec, idx := m.watchCursorPos()
	rows := m.overs
	if sec == secUnders {
		rows = m.unders
	} else if sec != secOvers {
		return nil
	}
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	return &rows[idx]
}

func (m Model) selectedUnpricedRow() *store.UnpricedRow {
	if m.view != viewWatches {
		return nil
	}
	sec, idx := m.watchCursorPos()
	if sec != secUnpriced || idx < 0 || idx >= len(m.unpriced) {
		return nil
	}
	return &m.unpriced[idx]
}

func (m Model) selectedWatchName() string {
	if w := m.selectedWatch(); w != nil {
		return w.Name
	}
	if r := m.selectedUnpricedRow(); r != nil {
		return r.Name
	}
	return ""
}

func (m Model) watchSectionBudgets() []int {
	regions := m.watchRegions()
	counts := make([]int, len(regions))
	for i, r := range regions {
		counts[i] = r.count
	}

	pool := max(m.visibleRows()-(2+3*2), 0)
	sec, _ := m.watchCursorPos()
	return sectionBudgets(counts, pool, int(sec))
}

func (m *Model) scrollWatchesIntoView() {
	regions := m.watchRegions()
	budgets := m.watchSectionBudgets()
	if m.watchTotalRows() > 0 {
		sec, idx := m.watchCursorPos()
		if b := budgets[int(sec)]; b > 0 {
			if idx < m.watchSecOffset[sec] {
				m.watchSecOffset[sec] = idx
			}
			if idx >= m.watchSecOffset[sec]+b {
				m.watchSecOffset[sec] = idx - b + 1
			}
		}
	}
	for i, r := range regions {
		m.watchSecOffset[r.sec] = min(max(m.watchSecOffset[r.sec], 0),
			max(r.count-budgets[i], 0))
	}
}

func (m *Model) jumpWatchSection(dir int) {
	regions := m.watchRegions()
	cur, _ := m.watchCursorPos()
	if i := int(cur) + dir; i >= 0 && i < len(regions) {
		m.cursor[paneCards] = regions[i].curStart
		m.focus = paneCards
		m.scrollIntoView()
	}
}

func (m Model) watchesLines(width int) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	regions := m.watchRegions()
	budgets := m.watchSectionBudgets()
	cursorSec, cursorIdx := m.watchCursorPos()

	hasCursor := m.focus == paneCards
	_, filtered := m.filterContainerID()

	var out []string
	for i, r := range regions {
		if i > 0 {
			out = append(out, "")
		}
		head := m.theme.Title.Render(r.sec.title()) + "  " + m.theme.Help.Render(r.sec.note())

		if frag := pagePhrase(m.watchSecOffset[r.sec], budgets[i], r.count, 0, 0, 0); frag != "" {
			head += m.theme.Help.Render(frag)
		}
		if r.count == 0 {

			if hasCursor && r.sec == cursorSec {
				head = ui.Restyle(fit(head, width), m.theme.Cursor)
			}
			out = append(out, head, m.theme.Help.Render(m.watchEmptyNote(r.sec, filtered)))
			continue
		}
		out = append(out, head)

		var t ui.Table
		switch r.sec {
		case secUnpriced:
			t = unpricedSectionTable(env, m.unpriced)
		case secUnders:
			t = watchSectionTable(env, m.unders)
		default:
			t = watchSectionTable(env, m.overs)
		}
		t.Env, t.Header = env, true
		lines := t.Lines()
		out = append(out, lines[0])
		off := m.watchSecOffset[r.sec]
		for k := off; k < min(off+budgets[i], r.count); k++ {
			line := lines[1+k]
			if hasCursor && r.sec == cursorSec && k == cursorIdx {

				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

func (m Model) watchEmptyNote(s watchSection, filtered bool) string {
	if !m.filter.empty() {
		return "none match " + m.filter.raw
	}
	if filtered {
		return "none in this collection"
	}
	return s.emptyNote()
}

func watchSectionTable(env ui.Env, rows []store.WatchStatus) ui.Table {
	t := ui.Table{Cols: []ui.Col{
		{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
		{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
		{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
		{Title: "THRESHOLD", Align: ui.Right},
		{Title: "PRICE", Align: ui.Right},
		{Title: "STATE", Align: ui.Left, Priority: 3},
	}}
	for _, w := range rows {
		price, state, style := "", "unpriced", env.Dim()
		if w.PriceUSD != nil {
			price = ui.Money(*w.PriceUSD)
			if w.Met() {

				state, style = "met", env.Bold()
			} else {
				state, style = "waiting", nil
			}
		}
		row := []ui.Cell{
			ui.C(w.Name), ui.C(ui.Printing(w.SetCode, w.CollectorNumber)),
			ui.C(ui.FinishTreated(w.Finish, w.Treatment)),
			ui.C(ui.Money(w.Threshold)), ui.C(price), ui.C(state),
		}
		if style != nil {
			t.AddStyled(style, row...)
		} else {
			t.Add(row...)
		}
	}
	return t
}

func unpricedSectionTable(env ui.Env, rows []store.UnpricedRow) ui.Table {
	t := ui.Table{Cols: []ui.Col{
		{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
		{Title: "ID", Align: ui.Left, Priority: 6, Style: env.PipsStyle()},
		{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
		{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
		{Title: "QTY", Align: ui.Right, Priority: 2},
		{Title: "HELD IN", Align: ui.Left, Priority: 5, Style: env.Dim()},
	}}
	for _, r := range rows {
		t.Add(ui.Cell{Text: r.Name, Style: env.Identity(r.ColorIdentity)},
			ui.C(ui.Pips(r.ColorIdentity)),
			ui.C(ui.Printing(r.SetCode, r.CollectorNumber)),
			ui.C(ui.FinishTreated(r.Finish, r.Treatment)),
			ui.C(ui.Qty(r.Copies)), ui.C(r.HeldIn))
	}
	return t
}

func (m Model) watchStatus() string {
	if m.watchTotalRows() == 0 {

		if m.emptyNote != "" {
			return m.theme.Help.Render(m.emptyNote)
		}
		sel := m.selectedContainer()
		if sel != nil && sel.Kind != kindAllCards {
			return m.theme.Help.Render(fmt.Sprintf(
				"nothing to watch in %s · All Cards shows every container", sel.Name))
		}
		if !m.filter.empty() {
			return m.theme.Help.Render(fmt.Sprintf(
				"nothing matches %s · esc clears the filter", m.filter.raw))
		}
		return m.theme.Help.Render(
			"no watches, and every card you own has a price · press w on a card in holdings")
	}
	sec, idx := m.watchCursorPos()
	line := sec.title() + " · empty"
	if n := m.watchCount(sec); n > 0 {
		line = fmt.Sprintf("%d/%d · %s", idx+1, n, sec.title())
	}
	if name := m.selectedItemName(); name != "" {
		line = name + " · " + line
	}
	line += " · sorted by " + m.sortLabel()
	if !m.filter.empty() {
		line += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	if min := m.floorMin(); min > 0 {
		line += fmt.Sprintf(" · floor %s (M cycles)", ui.Money(min))
	}
	return m.theme.Help.Render(line)
}

func (m Model) watchHeader() (title, totals string) {
	met := 0
	for _, rows := range [][]store.WatchStatus{m.overs, m.unders} {
		for _, w := range rows {
			if w.Met() {
				met++
			}
		}
	}
	return "WATCHES" + m.viewScope(), fmt.Sprintf("%s watches · %s met · %s unpriced",
		ui.Count(len(m.overs)+len(m.unders)), ui.Count(met), ui.Count(len(m.unpriced)))
}
