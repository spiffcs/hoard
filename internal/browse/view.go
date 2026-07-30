package browse

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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
func (m Model) visibleRows() int { return max(m.height-chromeRows, 1) }

// scrollIntoView moves the focused pane's window so the cursor is inside it.
func (m *Model) scrollIntoView() {
	rows := m.visibleRows()
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

// header labels each pane and puts the selected container's totals on the right,
// where the card pane they describe begins.
func (m Model) header(left, right int) string {
	name, totals := m.viewHeader()

	title := ui.Truncate(name, max(right-len(totals)-1, 0))
	gap := max(right-lipgloss.Width(title)-len(totals), 0)
	return titleStyle.Render(fit("COLLECTION", left)) +
		strings.Repeat(" ", paneGap) +
		titleStyle.Render(title) + strings.Repeat(" ", gap) + helpStyle.Render(totals)
}

// containerLines renders the left pane, one line per container.
func (m Model) containerLines(width int) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: true, Clamp: true}
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8},
			{Title: "VALUE", Align: ui.Right},
		},
	}
	for _, c := range m.containers {
		t.Add(ui.C(c.Name), ui.C(ui.Money(c.Value)))
	}
	return m.window(t.Lines(), paneContainers, width)
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
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: true, Clamp: true}
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
		// Board only means something inside a deck; against loose holdings it
		// would be a column of blanks.
		cols = append([]ui.Col{{Title: "BOARD", Align: ui.Left, Priority: 7, Style: env.Dim()}}, cols...)
	}
	t := ui.Table{Env: env, Header: true, Cols: cols}

	for _, c := range m.cards {
		finish := c.Finish
		if finish == "normal" {
			finish = "-"
		}
		cells := []ui.Cell{
			ui.C(c.Name), ui.C(c.SetCode + "/" + c.CollectorNumber), ui.C(finish),
			ui.C("×" + ui.Count(c.Quantity)), ui.C(ui.MoneyPtr(c.Price)),
			ui.C(estimated(ui.Money(c.Value), c.AltSource)),
		}
		if inDeck {
			cells = append([]ui.Cell{ui.C(c.Board)}, cells...)
		}
		t.Add(cells...)
	}
	return m.window(t.Lines(), paneCards, width)
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
			line = cursorStyle.Render(line)
		case i == m.cursor[p]:
			// The unfocused pane keeps a mark on its row so switching back is
			// not a hunt for where the cursor was.
			line = inactiveStyle.Render(line)
		}
		out = append(out, line)
	}
	return out
}

// statusLine is the filter bar when it is open, then any transient message,
// then the position within the pane.
func (m Model) statusLine() string {
	if m.confirm != nil {
		return errStyle.Render(m.confirm.prompt) + helpStyle.Render("  y/n")
	}
	if m.filtering {
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
	if s := m.emptyExplanation(); s != "" {
		return helpStyle.Render(s)
	}

	n := m.rowCount(m.focus)
	if n == 0 {
		return helpStyle.Render("nothing here")
	}
	pos := fmt.Sprintf("%d/%d · sorted by %s", m.cursor[m.focus]+1, n, m.sort)
	if !m.filter.empty() {
		pos += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	return helpStyle.Render(pos)
}

// emptyExplanation distinguishes a filter that matched nothing from a catalog
// with nothing to match against.
//
// Trait columns are NULL until update-prices has stored a Scryfall document, so
// on a hoard that has not been refreshed since the upgrade `rarity:mythic`
// correctly returns nothing — and looks exactly like a hoard containing no
// mythics. Only the count can tell those apart, and without saying so the
// feature reads as broken.
func (m Model) emptyExplanation() string {
	if len(m.cards) > 0 || m.filter.empty() || !m.filter.needsCatalog() {
		return ""
	}
	enriched, total, err := m.store.EnrichedCount()
	if err != nil || total == 0 {
		return ""
	}
	if enriched == 0 {
		return "no card details stored yet — run hoard update-prices to filter by rarity, type or colour"
	}
	if enriched < total {
		return fmt.Sprintf("no matches · %d of %d cards have details; update-prices fills the rest",
			enriched, total)
	}
	return "no matches"
}

func (m Model) helpLine() string {
	switch {
	case m.confirm != nil:
		return "y remove · any other key cancels"
	case m.filtering:
		return "type to filter · enter keep · esc clear · ctrl+u wipe · ↑/↓ move"
	case m.view == viewArbitrage && !m.arbLoaded && !m.arbLoading:
		return "enter fetch vendor prices · v next view · q quit"
	case m.view == viewArbitrage && m.arbLoading:
		return "esc cancel · q quit"
	case m.view != viewHoldings:
		// The editing keys do not apply to a hoard-wide analysis, so offering
		// them here would be an invitation to a refusal.
		return "v next view · ↑/↓ move · s sort · r reload · q quit"
	case m.focus == paneContainers:
		return "tab cards · / filter · s sort · v views · a add · d remove deck · u undo · q quit"
	}
	return "tab decks · enter detail · / filter · s sort · v views · a add · +/- qty · d remove · u undo · q quit"
}

// estimated marks a value Scryfall could not price, so a vendor estimate never
// passes for a Scryfall figure. Same convention as the CLI's listings.
func estimated(s, altSource string) string {
	if altSource == "" {
		return s
	}
	return s + "*"
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
