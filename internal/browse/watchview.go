package browse

// The watches screen: three tables on one pane. OVERS and UNDERS split the
// standing price thresholds by the direction each one waits for, and
// UNPRICED carries the holdings nothing can price — which was a view of its
// own until the owner asked for one screen. It is a plain instant database
// read like movers, so it keeps its place in the 'v' cycle; market keeps
// the last slot because the network view belongs at the end of the lap.
//
// The layout follows the market view's shape — each table owns a region of
// the pane, scrolls inside it, and keeps its own sort — but deliberately
// not its keying. Market's geometry is a [3] array indexed by market.Kind,
// an enum from another package with four values; it is safe only because a
// filter two files away drops the fourth, so an ordinary data change would
// land as an index-out-of-range in the middle of a frame. Here the regions
// are a slice built by the same loop that reads them, every section count
// comes from a total switch rather than an index, and the one enum that
// does index anything is watchSection, declared below with its own count.

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// watchSection names the three tables, in the order they render.
type watchSection int

const (
	secOvers watchSection = iota
	secUnders
	secUnpriced
	// watchSectionCount sizes every per-section array in the package. It is
	// the enum's own last value, so a fourth section grows the arrays with
	// it — the failure market's [3] can have is not reachable from here.
	watchSectionCount
)

// title and note are the heading each table renders and the phrase under it
// saying what the table is for.
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

// emptyNote is what a table with nothing in it says, standing — the
// container filter and the query override it with their own reason.
func (s watchSection) emptyNote() string {
	switch s {
	case secUnders:
		return "no watch is waiting for a drop"
	case secUnpriced:
		return "every card you own has a price"
	}
	return "no watch is waiting for a rise"
}

// sortColumns is this table's sort cycle, named as its headers read. Index
// 0 is the default and reproduces the order the rows already arrive in.
// The two watch tables share one cycle: they show the same columns and
// differ by one field, which the heading above them already carries.
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

// wantsUnder reports whether a watch waits for a fall, which is what puts
// it in the UNDERS table. It reads the op exactly the way store.Met does —
// "under" is the one direction named, everything else is an over — so a row
// can never be filed under a heading that disagrees with its own state
// column, and no row can go missing between the two tables.
func wantsUnder(w store.WatchStatus) bool { return w.Op == "under" }

// watchRegion is one table's place in the two spaces this screen keeps at
// once. count is how many rows it holds; curStart and span place it in the
// flat cursor space, and the two diverge because an empty table still takes
// one cursor slot, for its heading.
//
// That slot is the point: each table owns its own sort, and a table the
// cursor cannot reach is a table whose 's' has nowhere to land. It is also
// what keeps a screen with two empty tables reading as two empty tables
// rather than as a screen that failed to draw.
type watchRegion struct {
	sec      watchSection
	count    int
	curStart int
	span     int
}

// watchCount is how many rows one section holds. A total switch rather than
// an indexed array: it is the single place the section enum meets the data,
// and a switch cannot be out of range.
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

// watchRegions maps the three tables into both spaces, in render order.
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

// watchTotalRows is every data row on the screen — the count the header
// reports and the emptiness the pane tests, not the cursor's range, which
// is wider.
func (m Model) watchTotalRows() int {
	return len(m.overs) + len(m.unders) + len(m.unpriced)
}

// watchCursorSlots is how far the cursor can travel, which exceeds the row
// count by one slot for every empty table — see watchRegion.
func (m Model) watchCursorSlots() int {
	n := 0
	for _, r := range m.watchRegions() {
		n += r.span
	}
	return n
}

// watchCursorPos maps the flat cursor to its section and the index within
// it. On an empty table the index is 0 and addresses no row: callers that
// read data must check the section's count, which is what selectedWatch and
// selectedUnpricedRow do.
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

// firstWatchCursor is where the cursor belongs after the rows underneath
// change: the first table that actually has something in it. Landing on an
// empty heading is a place the user can choose to go, never one the screen
// drops them into.
func (m Model) firstWatchCursor() int {
	for _, r := range m.watchRegions() {
		if r.count > 0 {
			return r.curStart
		}
	}
	return 0
}

// selectedWatch is the watch under the cards cursor — nil off this screen,
// on the UNPRICED table, or on a heading with no rows under it.
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

// selectedUnpricedRow is selectedWatch's twin for the third table: the
// unpriced holding under the cursor, nil anywhere else. The two are
// mutually exclusive by construction, which is how every caller that has to
// tell a watch row from an unpriced one asks the question.
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

// selectedWatchName names the row under the cursor for the position line,
// empty on a heading with nothing under it.
func (m Model) selectedWatchName() string {
	if w := m.selectedWatch(); w != nil {
		return w.Name
	}
	if r := m.selectedUnpricedRow(); r != nil {
		return r.Name
	}
	return ""
}

// watchSectionBudgets divides the pane's rows among the three tables — the
// market pane's arithmetic, shared.
func (m Model) watchSectionBudgets() []int {
	regions := m.watchRegions()
	counts := make([]int, len(regions))
	for i, r := range regions {
		counts[i] = r.count
	}
	// Furniture: a separator above each table but the first, and a title
	// plus one line (the column header, or the empty note) per table.
	pool := max(m.visibleRows()-(2+3*2), 0)
	sec, _ := m.watchCursorPos()
	return sectionBudgets(counts, pool, int(sec))
}

// scrollWatchesIntoView keeps the cursor visible inside its table's region,
// and every table's offset inside its rows — this pane's counterpart to the
// generic offset walk.
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

// jumpWatchSection moves the cursor to the top of the next (+1) or previous
// (-1) table — the ]/[ keys, so reaching UNPRICED never means walking two
// tables' overflow row by row. Empty tables are stops too: they keep their
// heading, and their heading is where the table's own keys land.
func (m *Model) jumpWatchSection(dir int) {
	regions := m.watchRegions()
	cur, _ := m.watchCursorPos()
	if i := int(cur) + dir; i >= 0 && i < len(regions) {
		m.cursor[paneCards] = regions[i].curStart
		m.focus = paneCards
		m.scrollIntoView()
	}
}

// watchesLines renders the three tables in fixed regions: each gets a share
// of the height and scrolls its own rows inside it, rather than the pane
// scrolling as one document — so the three-table shape holds still while
// the cursor digs into any one of them. Each table keeps its own title row
// and column headers, because a watch row and an unpriced holding do not
// share a single honest header. An overflowing table says where it is in
// its rows on the title line; an empty one keeps its title over a note, so
// a table emptied by the container filter reads as filtered, not missing.
func (m Model) watchesLines(width int) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	regions := m.watchRegions()
	budgets := m.watchSectionBudgets()
	cursorSec, cursorIdx := m.watchCursorPos()
	// The bar only draws while the pane has focus — an Inactive mark on an
	// analytical row reads as a dimmed card, not a remembered place.
	hasCursor := m.focus == paneCards
	_, filtered := m.filterContainerID()

	var out []string
	for i, r := range regions {
		if i > 0 {
			out = append(out, "")
		}
		head := m.theme.Title.Render(r.sec.title()) + "  " + m.theme.Help.Render(r.sec.note())
		// No page arithmetic here: this screen scrolls its tables, it does
		// not page them, so only the on-screen fragment can apply.
		if frag := pagePhrase(m.watchSecOffset[r.sec], budgets[i], r.count, 0, 0, 0); frag != "" {
			head += m.theme.Help.Render(frag)
		}
		if r.count == 0 {
			// The heading is the selection when there is nothing under it,
			// so the bar goes on the heading itself — the table's own 's'
			// acts on whatever the bar is sitting on, and without it an
			// empty table would look unreachable while answering the key.
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
				// Restyle, not strip: the bar spans the row and the identity
				// tints show through it, same as the generic pane windowing.
				line = ui.Restyle(fit(line, width), m.theme.Cursor)
			}
			out = append(out, line)
		}
	}
	return out
}

// watchEmptyNote is one table's reason for being empty: the query first,
// then the container filter, then the standing reason — in that order,
// because "no watch is waiting for a rise" would be a lie about the hoard
// when a typed query is what emptied the table.
func (m Model) watchEmptyNote(s watchSection, filtered bool) string {
	if !m.filter.empty() {
		return "none match " + m.filter.raw
	}
	if filtered {
		return "none in this collection"
	}
	return s.emptyNote()
}

// watchSectionTable lays out one direction's watches. The direction itself
// is not a column: the heading above the table already carries it, so
// THRESHOLD prints the line alone rather than repeating "over" down every
// row of a table named OVERS.
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
				// Met rows are the reason the screen exists; they carry the
				// only emphasis.
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

// unpricedSectionTable lays out the holdings nothing can price.
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

// watchStatus is the status line for this screen: the selection, its place
// within its own table — not the flat cursor space, which spans three
// rankings — and the sort that table is under.
func (m Model) watchStatus() string {
	if m.watchTotalRows() == 0 {
		// A trait query the catalog cannot answer yet has its own
		// explanation, and it outranks every reason below: "no watches"
		// would send the reader looking for rows that exist.
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

// watchHeader is the right pane's title and summary for this screen.
//
// The over/under split is deliberately not in the summary: it is two
// headings the eye already has, and spelling it out here cost enough width
// to truncate the pane's own title away on a narrow terminal. What the
// header adds is what no single table shows — every watch, how many are
// met, and the third table's size.
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
