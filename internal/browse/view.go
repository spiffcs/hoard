package browse

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// The palette mirrors internal/tui and internal/ui: bold and faint only, plus
// one reverse for the cursor. No colour, so the design survives a monochrome
// terminal and does not fight the user's own theme.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	helpStyle     = lipgloss.NewStyle().Faint(true)
	errStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	cursorStyle   = lipgloss.NewStyle().Reverse(true)
	inactiveStyle = lipgloss.NewStyle().Faint(true)
)

// Layout constants. The container pane is fixed and the card pane takes the
// rest: container names are short and repetitive, card rows carry six columns
// and are what the eye is actually reading.
const (
	containerPaneWidth = 30
	paneGap            = 2
	minCardPaneWidth   = 24

	// chromeRows is everything not a list row: the two pane titles, the rule,
	// the status line and the help line.
	chromeRows = 5
)

// View renders the whole screen.
func (m Model) View() string {
	if !m.ready {
		// Before the first WindowSizeMsg there is no width to lay out against,
		// and guessing 80 would make the first frame jump.
		return ""
	}

	if m.detail != nil {
		return m.detailView()
	}

	left, right := m.paneWidths()
	leftLines := m.containerLines(left)
	rightLines := m.rightLines(right)

	var b strings.Builder
	b.WriteString(m.header(left, right) + "\n")
	for i := range m.visibleRows() {
		b.WriteString(fit(lineAt(leftLines, i), left))
		b.WriteString(strings.Repeat(" ", paneGap))
		// Right-hand lines are trimmed rather than padded: trailing spaces to
		// the edge of the terminal are invisible but make the frame wider than
		// it needs to be, and on the last column they can trigger a wrap.
		b.WriteString(strings.TrimRight(fit(lineAt(rightLines, i), right), " "))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	b.WriteString(m.statusLine() + "\n")
	// The palette's match rows sit between its input line (the status slot)
	// and the help line — the drawer the panes shrank to make room for.
	if m.palette != nil {
		for _, line := range m.paletteLines(m.width) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString(helpStyle.Render(ui.Truncate(m.helpLine(), m.width)))
	return b.String()
}

// detailView renders the card overlay in place of the two panes.
//
// It replaces the panes rather than floating over them: the oracle text and
// price history need the width, and a box drawn over a table leaves fragments
// of card names showing around its edges that read as corruption.
func (m Model) detailView() string {
	lines := m.detailLines(*m.detail, m.width)

	var b strings.Builder
	for i := range m.visibleRows() + 1 {
		b.WriteString(strings.TrimRight(lineAt(lines, i), " "))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	if n := len(lines) - (m.visibleRows() + 1); n > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("%d more lines — widen or lengthen the window", n)))
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(ui.Truncate("esc back · q quit", m.width)))
	return b.String()
}

// paneWidths splits the terminal between the two panes.
//
// The container pane gives way first on a narrow terminal, down to nothing: the
// cards are the content, and a screen too narrow for both should show the one
// the cursor is likely to be in rather than squeezing both into uselessness.
func (m Model) paneWidths() (left, right int) {
	left = containerPaneWidth
	if m.width-left-paneGap < minCardPaneWidth {
		left = max(m.width-paneGap-minCardPaneWidth, 0)
	}
	right = max(m.width-left-paneGap, 0)
	return left, right
}

// visibleRows is how many list rows fit between the header and the footer.
// visibleRows is how many body rows the panes get; the palette drawer, when
// open, takes its rows from here so the frame keeps its height.
func (m Model) visibleRows() int { return max(m.height-chromeRows-m.paletteRows(), 1) }

// scrollIntoView moves the focused pane's window so the cursor is inside it.
//
// A pane shows one fewer data row than visibleRows: the column-title line is
// drawn inside the pane, not the chrome. Counting it here would let the cursor
// rest one row past the border, highlighted but invisible.
func (m *Model) scrollIntoView() {
	rows := max(m.visibleRows()-1, 1)
	for p := range m.offset {
		if m.cursor[p] < m.offset[p] {
			m.offset[p] = m.cursor[p]
		}
		if m.cursor[p] >= m.offset[p]+rows {
			m.offset[p] = m.cursor[p] - rows + 1
		}
		m.offset[p] = max(m.offset[p], 0)
	}
}

// headerSparkCells is how wide the hoard-value sparkline draws. Narrower than
// the detail view's: it shares the header row with the title and the totals,
// both of which matter more.
const headerSparkCells = 24

// header labels each pane and puts the selected container's totals on the right,
// where the card pane they describe begins.
func (m Model) header(left, right int) string {
	name, totals := m.viewHeader()

	title := ui.Truncate(name, max(right-len(totals)-1, 0))
	gap := max(right-lipgloss.Width(title)-len(totals), 0)

	// The hoard-value sparkline sits in the gap, beside the totals it
	// explains. It yields first on a narrow terminal: the title and the
	// numbers are the header's job, the chart is a bonus.
	middle := strings.Repeat(" ", gap)
	if spark := m.valueSpark(); spark != "" && gap >= lipgloss.Width(spark)+2 {
		middle = strings.Repeat(" ", gap-lipgloss.Width(spark)-1) +
			helpStyle.Render(spark) + " "
	}
	return titleStyle.Render(fit("COLLECTION", left)) +
		strings.Repeat(" ", paneGap) +
		titleStyle.Render(title) + middle + helpStyle.Render(totals)
}

// valueSpark is the hoard's worth over time as a one-line chart: empty
// outside the holdings view, or until two snapshots exist to draw a line
// between. A "≈" prefix marks a chart still mostly built from seeded points —
// values reconstructed from today's quantities, estimates rather than
// observations. Seeded rows never leave the series, so the marker clears not
// when they vanish but when genuine observations outnumber them.
func (m Model) valueSpark() string {
	if m.view != viewHoldings || len(m.valueSeries) < 2 {
		return ""
	}
	points := make([]ui.TimePoint, len(m.valueSeries))
	seeded := 0
	for i, p := range m.valueSeries {
		points[i] = ui.TimePoint{AsOf: p.AsOf, Value: p.Total}
		if p.Seeded {
			seeded++
		}
	}
	spark := ui.Spark(ui.Resample(points, headerSparkCells), headerSparkCells)
	if spark == "" {
		return ""
	}
	if seeded*2 >= len(m.valueSeries) {
		spark = "≈" + spark
	}
	return spark
}

// paneLines renders one pane's table. Every pane shares the same Env, the
// header row, the zero-width guard and the windowing; only the columns and the
// rows differ, so those are all a renderer supplies. Before this, two of the
// five renderers guarded zero width and three did not.
func (m Model) paneLines(p pane, width int, build func(env ui.Env) ui.Table) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: true, Clamp: true}
	t := build(env)
	t.Env = env
	t.Header = true
	return m.window(t.Lines(), p, width)
}

// containerLines renders the left pane, one line per container.
func (m Model) containerLines(width int) []string {
	return m.paneLines(paneContainers, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8},
			{Title: "VALUE", Align: ui.Right},
		}}
		for _, c := range m.containers {
			t.Add(ui.C(c.Name), ui.C(ui.Money(c.Value)))
		}
		return t
	})
}

// rightLines renders whichever analysis the right pane is showing.
func (m Model) rightLines(width int) []string {
	switch m.view {
	case viewMovers:
		return m.moversLines(width)
	case viewUnpriced:
		return m.unpricedLines(width)
	case viewArbitrage:
		return m.arbitrageLines(width)
	}
	return m.cardLines(width)
}

// cardLines renders the selected container's holdings.
func (m Model) cardLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		inDeck := false
		if sel := m.selectedContainer(); sel != nil {
			inDeck = sel.Kind != store.KindCollection
		}

		cols := []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "VALUE", Align: ui.Right},
		}
		if inDeck {
			// Board only means something inside a deck; against loose holdings
			// it would be a column of blanks. It sits beside NAME: the name is
			// what the eye reads first, and the board qualifies it.
			cols = slices.Insert(cols, 1,
				ui.Col{Title: "BOARD", Align: ui.Left, Priority: 7, Style: env.Dim()})
		}
		t := ui.Table{Cols: cols}

		for _, c := range m.cards {
			finish := ui.Finish(c.Finish)
			cells := []ui.Cell{
				ui.C(c.Name), ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(ui.Qty(c.Quantity)), ui.C(ui.MoneyPtr(c.Price)),
				ui.C(ui.Estimated(ui.Money(c.Value), c.AltSource)),
			}
			if inDeck {
				cells = slices.Insert(cells, 1, ui.C(c.Board))
			}
			t.Add(cells...)
		}
		return t
	})
}

// window slices a pane's rendered lines to what fits, and draws the cursor.
//
// The table's own lines are used rather than re-rendering the selected row
// specially, so the cursor cannot disagree with the layout about column widths.
// Lines carry no trailing padding, so the highlight is padded to the pane width
// first — otherwise the reverse-video bar stops at the last character and the
// selected row looks ragged.
func (m Model) window(lines []string, p pane, width int) []string {
	if len(lines) == 0 {
		return nil
	}
	rows := lines[1:] // line 0 is the header
	start := min(m.offset[p], max(len(rows)-1, 0))
	end := min(start+m.visibleRows()-1, len(rows))

	out := make([]string, 0, m.visibleRows())
	out = append(out, lines[0])
	for i := start; i < end; i++ {
		line := fit(rows[i], width)
		switch {
		case i == m.cursor[p] && m.focus == p:
			// The row's own styling is stripped first: a dim cell's reset would
			// end the reverse video mid-line, leaving a selection bar that stops
			// at whichever column happened to be styled.
			line = cursorStyle.Render(ansi.Strip(line))
		case i == m.cursor[p]:
			// The unfocused pane keeps a mark on its row so switching back is
			// not a hunt for where the cursor was.
			line = inactiveStyle.Render(ansi.Strip(line))
		}
		out = append(out, line)
	}
	return out
}

// statusLine is the filter bar when it is open, then any transient message,
// then the position within the pane.
// statusLine is the one line below the rule, and this function is the whole
// precedence contract for it: confirm → prompt → filter bar → transient
// status → (op progress, when the op layer lands) → arbitrage → empty note
// → position. New writers claim a slot here, never an ad-hoc check
// elsewhere, or the ordering drifts back into scattered ifs.
func (m Model) statusLine() string {
	switch m.mode() {
	case modeConfirm:
		return errStyle.Render(m.confirm.prompt) + helpStyle.Render("  y/n")
	case modePrompt:
		return m.promptLine()
	case modePalette:
		return ": " + m.palette.query + "▏"
	case modeFilter:
		bar := "/" + m.filterText + "▏"
		if m.filterErr != "" {
			return bar + "  " + errStyle.Render(m.filterErr)
		}
		return bar + helpStyle.Render(fmt.Sprintf("  %d match", len(m.cards)))
	}
	if m.status != "" {
		if m.statusErr {
			return errStyle.Render(m.status)
		}
		return m.status
	}
	if m.view == viewArbitrage {
		return m.arbitrageStatus()
	}
	if m.emptyNote != "" {
		return helpStyle.Render(m.emptyNote)
	}

	n := m.rowCount(m.focus)
	if n == 0 {
		return helpStyle.Render("nothing here")
	}
	pos := fmt.Sprintf("%d/%d · sorted by %s", m.cursor[m.focus]+1, n, m.sortLabel())
	if !m.filter.empty() {
		pos += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	if note := m.estimateNote(); note != "" {
		pos += " · " + note
	}
	return helpStyle.Render(pos)
}

// estimateNote explains the asterisked values, naming the vendors involved.
// Empty when every visible price came from Scryfall, so the common case pays
// no width for it.
func (m Model) estimateNote() string {
	if m.view != viewHoldings {
		return ""
	}
	sources := map[string]bool{}
	for _, c := range m.cards {
		if c.AltSource != "" {
			sources[c.AltSource] = true
		}
	}
	if len(sources) == 0 {
		return ""
	}
	return "* estimated from " + strings.Join(slices.Sorted(maps.Keys(sources)), ", ") + " via MTGJSON"
}

func (m Model) helpLine() string {
	switch {
	// Wording stays "remove" while removals are the only confirms; it
	// generalizes when another action stages one.
	case m.confirm != nil:
		return "y remove · any other key cancels"
	case m.prompt != nil:
		return "type the answer · enter accept · esc cancel · ctrl+u wipe"
	case m.palette != nil:
		return "enter run · esc close · ↑/↓ choose · type to narrow"
	case m.filtering:
		return "type to filter · enter keep · esc clear · ctrl+u wipe · ↑/↓ move"
	case m.view == viewArbitrage && !m.arbLoaded && !m.arbLoading:
		return "enter fetch vendor prices · v next view · q quit"
	case m.view == viewArbitrage && m.arbLoading:
		return "esc cancel · q quit"
	case m.view != viewHoldings:
		// The editing keys do not apply to a hoard-wide analysis, so offering
		// them here would be an invitation to a refusal.
		return "v next view · : commands · ↑/↓ move · s sort · S reverse · r reload · q quit"
	case m.focus == paneContainers:
		return "tab cards · / filter · : commands · s sort · S reverse · v views · a add · d remove deck · u undo · q quit"
	}
	return "tab decks · enter detail · / filter · : commands · s sort · S reverse · v views · a add · +/- qty · d remove · u undo · q quit"
}

// lineAt is lines[i], or blank past the end, so both panes can be walked
// together without checking which is longer.
func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// fit forces s to exactly w display cells, padding or truncating.
//
// Truncation is the backstop that keeps the frame inside the terminal. A
// column's Min width can exceed a narrow pane's whole budget — two columns with
// minimums of 8 and 7 cannot fit 14 cells however the layout is solved — and
// without a hard clamp here the overflow wraps, which puts one row on two lines
// and breaks the one-line-per-row contract the cursor depends on.
//
// Width is measured as rendered, so ANSI already in the string does not count.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := lipgloss.Width(s)
	if n < w {
		return s + strings.Repeat(" ", w-n)
	}
	if n > w {
		return ui.Truncate(s, w)
	}
	return s
}
