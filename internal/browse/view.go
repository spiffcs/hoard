package browse

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
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

	if m.addChild != nil {
		// The cascade owns the frame. The only browse surface that can
		// coexist is the quit-mid-op confirm (ctrl+c with an op running),
		// appended beneath so the question never hides the cascade state
		// it is asking about.
		v := m.addChild.View()
		if m.confirm != nil {
			v += "\n" + m.theme.Err.Render(m.confirm.prompt) + m.theme.Help.Render("  y/n")
		}
		return v
	}

	if m.detail != nil {
		return m.detailView()
	}

	if m.text != nil {
		return m.textViewRender()
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
	m.writeHelp(&b, m.helpLine())
	return b.String()
}

// writeHelp renders the wrapped help rows, last line without a newline.
// While the palette is open, the highlighted command's description renders
// beneath — the drawer names the verbs, this line says what they do.
func (m Model) writeHelp(b *strings.Builder, help string) {
	for i, line := range wrapHelp(help, m.width) {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.theme.Help.Render(line))
	}
	if desc := m.paletteDesc(); desc != "" {
		b.WriteString("\n" + m.theme.Help.Render(ui.Truncate(desc, m.width)))
	}
}

// detailView renders the card overlay in place of the two panes.
//
// It replaces the panes rather than floating over them: the oracle text and
// price history need the width, and a box drawn over a table leaves fragments
// of card names showing around its edges that read as corruption. The card
// image sits to the right of the card frame alone — the frame is what it
// illustrates, and the analysis below (prices, bids, comps) takes the full
// width. A terminal too narrow for the pair stacks instead: the card's
// details, then the image, then hoard's own facts — and one too narrow
// even for the image alone goes text-only.
func (m Model) detailView() string {
	img := m.detail.image
	if len(img) == 0 && m.detail.imagePending {
		// The art is coming: hold its space so HELD, PRICE and COMPS
		// render in their final positions from the first frame.
		img = blankImage(m.detailImageCols())
	}
	var lines []string
	switch {
	case len(img) > 0 && m.width >= imageCols+50:
		// The image sits beside the card frame only; the hoard's analysis
		// below gets the full width — the bid and comps rows are wide, and
		// an image column clipping them cost more than it decorated
		// (observed live).
		textW := m.width - imageCols - 2
		lines = besideImage(img, m.cardFrameLines(*m.detail, textW), textW)
		lines = append(lines, m.hoardLines(*m.detail, m.width)...)
	case len(img) > 0 && m.width >= imageCols:
		lines = append(m.cardFrameLines(*m.detail, m.width), "")
		lines = append(lines, img...)
		lines = append(lines, m.hoardLines(*m.detail, m.width)...)
	default:
		lines = m.detailLines(*m.detail, m.width)
	}

	var b strings.Builder
	for i := range m.visibleRows() + 1 {
		b.WriteString(strings.TrimRight(lineAt(lines, i), " "))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	// The slot under the rule: an active surface — a staged confirm, an
	// open prompt, the palette's input line — outranks the more-lines note,
	// so commands run from the overlay stay visible over it. A running
	// operation or a transient status claims it next: a refresh started
	// from the overlay reports here exactly as it would under the panes —
	// progress while it runs, the summary after (its absence read as the
	// command not firing, observed live).
	switch {
	case m.confirm != nil || m.prompt != nil || m.palette != nil:
		b.WriteString(m.statusLine())
	case m.op != nil || m.status != "":
		b.WriteString(m.statusLine())
	default:
		if n := len(lines) - (m.visibleRows() + 1); n > 0 {
			b.WriteString(m.theme.Help.Render(fmt.Sprintf("%d more lines · widen or lengthen the window", n)))
		}
	}
	b.WriteString("\n")
	if m.palette != nil {
		for _, line := range m.paletteLines(m.width) {
			b.WriteString(line + "\n")
		}
	}
	m.writeHelp(&b, m.helpLine())
	return b.String()
}

// besideImage lays the image block to the right of the text block: each
// text line is padded to the text column's width so the image's left edge
// stays straight, and rows past the image's bottom need no padding at all.
func besideImage(img, text []string, textW int) []string {
	out := make([]string, 0, max(len(img), len(text)))
	for i := range max(len(img), len(text)) {
		var left string
		if i < len(text) {
			left = text[i]
		}
		if i >= len(img) {
			out = append(out, left)
			continue
		}
		out = append(out, fit(left, textW)+"  "+img[i])
	}
	return out
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

// wrapHelp breaks a help line between its " · " entries so every key stays
// visible on a narrow terminal, instead of truncating off the edge of the
// screen. A single entry longer than the width stands alone and clips.
func wrapHelp(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	cur := ""
	for _, part := range strings.Split(s, " · ") {
		joined := part
		if cur != "" {
			joined = cur + " · " + part
		}
		if cur == "" || lipgloss.Width(joined) <= width {
			cur = joined
			continue
		}
		lines = append(lines, cur)
		cur = part
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// helpRows is how many rows the wrapped help line costs right now,
// including the palette's description line when one is showing.
func (m Model) helpRows() int {
	if !m.ready {
		return 1
	}
	rows := len(wrapHelp(m.helpLine(), m.width))
	if m.paletteDesc() != "" {
		rows++
	}
	return rows
}

// visibleRows is how many body rows the panes get; the palette drawer and
// any extra help rows take theirs from here so the frame keeps its height.
func (m Model) visibleRows() int {
	return max(m.height-chromeRows-m.paletteRows()-(m.helpRows()-1), 1)
}

// scrollIntoView moves the focused pane's window so the cursor is inside it.
//
// A pane shows one fewer data row than visibleRows: the column-title line is
// drawn inside the pane, not the chrome. Counting it here would let the cursor
// rest one row past the border, highlighted but invisible.
func (m *Model) scrollIntoView() {
	rows := max(m.visibleRows()-1, 1)
	for p := range m.offset {
		if pane(p) == paneCards && m.view == viewMarket {
			// The market pane scrolls per section, not as one document;
			// m.offset stays untouched for it.
			m.scrollMarketIntoView()
			continue
		}
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
// where the card pane they describe begins. The focused pane's title takes
// the accent — before this, focus was only discoverable by finding which
// cursor was solid.
func (m Model) header(left, right int) string {
	name, totals := m.viewHeader()

	totals += m.opBadge()
	title := ui.Truncate(name, max(right-lipgloss.Width(totals)-1, 0))
	gap := max(right-lipgloss.Width(title)-lipgloss.Width(totals), 0)
	return m.paneTitle(paneContainers).Render(fit("COLLECTION", left)) +
		strings.Repeat(" ", paneGap) +
		m.paneTitle(paneCards).Render(title) + strings.Repeat(" ", gap) + m.theme.Help.Render(totals)
}

// paneTitle is the title style focus decides: accent when the cursor lives
// here, plain title bold otherwise. Bold rather than Inactive for the
// unfocused side — both panes stay readable; only one advertises the hand.
func (m Model) paneTitle(p pane) lipgloss.Style {
	if m.focus == p {
		return m.theme.Accent
	}
	return m.theme.Title
}

// paneLines renders one pane's table. Every pane shares the same Env, the
// header row, the zero-width guard and the windowing; only the columns and the
// rows differ, so those are all a renderer supplies. Before this, two of the
// five renderers guarded zero width and three did not.
func (m Model) paneLines(p pane, width int, build func(env ui.Env) ui.Table) []string {
	if width <= 0 {
		return nil
	}
	env := ui.Env{Width: width, Color: m.env.Color, Clamp: true}
	t := build(env)
	t.Env = env
	t.Header = true
	return m.window(t.Lines(), p, width)
}

// containerLines renders the left pane, one line per container. On views
// that grey out containers with nothing to show, the ineligible rows dim —
// the cursor skips them, and a row that cannot be selected must look it.
func (m Model) containerLines(width int) []string {
	return m.paneLines(paneContainers, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8},
			{Title: "VALUE", Align: ui.Right},
		}}
		for i, c := range m.containers {
			if !m.containerEligible(i) {
				t.AddStyled(env.Dim(), ui.C(c.Name), ui.C(ui.Money(c.Value)))
				continue
			}
			t.Add(ui.C(c.Name), ui.C(ui.Money(c.Value)))
		}
		return t
	})
}

// rightLines renders whichever analysis the right pane is showing.
func (m Model) rightLines(width int) []string {
	switch m.view {
	case viewWatches:
		return m.watchesLines(width)
	case viewMovers:
		return m.moversLines(width)
	case viewUnpriced:
		return m.unpricedLines(width)
	case viewMarket:
		return m.marketLines(width)
	}
	return m.cardLines(width)
}

// cardLines renders the selected container's holdings.
func (m Model) cardLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		inDeck := false
		if sel := m.selectedContainer(); sel != nil {
			inDeck = sel.Kind == store.KindDeck
		}

		cols := []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			// The identity pips sit beside the name the way the mana symbols
			// do on the card. Pure meaning-bearing ornament, so it is the
			// first column a narrow terminal gives up.
			{Title: "ID", Align: ui.Left, Priority: 8, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 2},
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "VALUE", Align: ui.Right},
		}
		if inDeck {
			// Board only means something inside a deck; against loose holdings
			// it would be a column of blanks. It follows the name block (name
			// and pips) that the eye reads first, and qualifies it.
			cols = slices.Insert(cols, 2,
				ui.Col{Title: "BOARD", Align: ui.Left, Priority: 7, Style: env.Dim()})
		}
		t := ui.Table{Cols: cols}

		for _, c := range m.cards {
			finish := ui.Finish(c.Finish)
			cells := []ui.Cell{
				{Text: c.Name, Style: env.Identity(c.ColorIdentity)},
				ui.C(ui.Pips(c.ColorIdentity)),
				ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(ui.Qty(c.Quantity)), ui.C(ui.MoneyPtr(c.Price)),
				ui.C(ui.Money(c.Value)),
			}
			if inDeck {
				cells = slices.Insert(cells, 2, ui.C(c.Board))
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
			// Restyle rather than strip-and-reverse: the bar spans the row
			// because the reverse is re-asserted past every embedded reset,
			// and the row's identity tints show through it.
			line = ui.Restyle(line, m.theme.Cursor)
		case i == m.cursor[p] && (p == paneContainers || m.view == viewHoldings):
			// The unfocused pane keeps a mark on its row so switching back is
			// not a hunt for where the cursor was. Not on the analytical
			// views' rows, though: moving the container cursor resets the
			// card cursor to the top, so the mark always sat on row one —
			// reading as a dimmed first card, not a remembered place —
			// and dim already means ineligible in the pane beside it.
			line = ui.Restyle(line, m.theme.Inactive)
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
		return m.theme.Err.Render(m.confirm.prompt) + m.theme.Help.Render("  y/n")
	case modePrompt:
		return m.promptLine()
	case modePalette:
		return ": " + m.palette.query + "▏"
	case modeFilter:
		bar := "/" + m.filterText + "▏"
		if m.filterErr != "" {
			return bar + "  " + m.theme.Err.Render(m.filterErr)
		}
		return bar + m.theme.Help.Render(fmt.Sprintf("  %d match", len(m.cards)))
	}
	if m.status != "" {
		if m.statusErr {
			return m.theme.Err.Render(m.status)
		}
		return m.status
	}
	// The op slot sits below transient statuses on purpose: pressing 's'
	// deserves its "sorted by value" beat, and the header badge keeps the
	// op visible meanwhile.
	if m.op != nil {
		return m.opStatus()
	}
	if m.view == viewMarket {
		return m.marketStatus()
	}
	if m.emptyNote != "" {
		return m.theme.Help.Render(m.emptyNote)
	}

	n := m.rowCount(m.focus)
	if n == 0 {
		// An empty analytical view says how to fill it — the feedback that
		// turned "nothing here" into a dead end during dogfooding. When the
		// container filter is what emptied it, say that instead: "no price
		// movement" would be a lie about the hoard.
		sel := m.selectedContainer()
		scoped := sel != nil && sel.Kind != kindAllCards && m.view != viewHoldings
		switch m.view {
		case viewMovers:
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no price movement in %s this window · All cards shows every container", sel.Name))
			}
			return m.theme.Help.Render(
				"no price movement in this window · F fetches prices and 90 days of history · W widens the window")
		case viewWatches:
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no watches shown for %s · All cards shows every container", sel.Name))
			}
			return m.theme.Help.Render(
				"no watches · press w on a card in holdings, or : then \"Add a watch\"")
		case viewUnpriced:
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no unpriced cards in %s · All cards shows every container", sel.Name))
			}
			return m.theme.Help.Render("every card you own has a price")
		}
		return m.theme.Help.Render("nothing here")
	}
	pos := fmt.Sprintf("%d/%d · sorted by %s", m.cursor[m.focus]+1, n, m.sortLabel())
	if !m.filter.empty() {
		pos += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	if min := m.floorMin(); min > 0 {
		pos += fmt.Sprintf(" · floor %s (M cycles)", ui.Money(min))
	}
	return m.theme.Help.Render(pos)
}

func (m Model) helpLine() string {
	switch {
	case m.confirm != nil:
		if m.confirm.help != "" {
			return m.confirm.help
		}
		return "y confirm · any other key cancels"
	case m.prompt != nil:
		if m.prompt.help != "" {
			return m.prompt.help
		}
		return "type the answer · enter accept · esc cancel · ctrl+u wipe"
	case m.palette != nil:
		return "enter run · esc close · ↑/↓ choose · type to narrow"
	case m.filtering && m.watchPick:
		return "type to find the card · ↑/↓ move · enter watch it · tab decks/binders · esc cancel"
	case m.filtering:
		return "type to filter · enter keep · esc clear · ctrl+u wipe · ↑/↓ move"
	case m.watchPick:
		return "↑/↓ pick the card · enter watch it · tab decks/binders · / filter · esc cancel"
	case m.detail != nil:
		help := ""
		if len(m.detail.holdings) > 1 {
			help = "↑/↓ held printings · "
		}
		if m.openURL != nil && len(m.detail.links) > 0 {
			help += "←/→ links · enter open in browser · "
		}
		return help + "esc back · ctrl+c quit"
	case m.text != nil:
		return "↑/↓ scroll · pgup/pgdn page · g/G ends · esc back · ctrl+c quit"
	case m.view == viewMarket && !m.marketLoaded && !m.marketLoading:
		return "F fetch vendor prices · v next view · q quit"
	case m.view == viewMarket && m.marketLoading:
		return "esc cancel · ctrl+c quit"
	case m.view == viewMarket:
		return "enter detail · ]/[ next/prev table · b comps buy/sell · F refetch quotes · M floor · tab collections · v next view · : commands · ↑/↓ move · q quit"
	case m.view == viewWatches:
		// Each analytical view leads with its own verbs — a generic line
		// here once hid that watches can be added at all.
		return "w edit threshold · d remove · : add a watch · enter detail · M floor · tab collections · v next view · ↑/↓ move · q quit"
	case m.view == viewMovers:
		return "W lookback 7/30/90 days · F update prices + history · enter detail · M floor · tab collections · v next view · : commands · ↑/↓ move · s sort · q quit"
	case m.view == viewUnpriced:
		return "F refresh prices · enter detail · tab collections · v next view · : commands · ↑/↓ move · s sort · q quit"
	case m.view != viewHoldings:
		// The editing keys do not apply to an analytical view, so offering
		// them here would be an invitation to a refusal.
		return "tab collections · v next view · : commands · F fetch data · ↑/↓ move · s sort · S reverse · q quit"
	case m.focus == paneContainers:
		// The merged all-cards row is read-only, so its help drops the
		// verbs that would only ever answer with a refusal.
		if sel := m.selectedContainer(); sel != nil && sel.Kind == kindAllCards {
			return "tab cards · n new binder · a add cards · : import/export · / filter · M floor · F refresh prices · v views · q quit"
		}
		return "tab cards · n new binder · a add cards · R rename · d remove · : import/export · / filter · M floor · F refresh prices · v views · u undo · q quit"
	}
	if sel := m.selectedContainer(); sel != nil && sel.Kind == kindAllCards {
		return "tab decks · enter detail · / filter · M floor · : commands · s sort · S reverse · F refresh prices · v views · a add · q quit"
	}
	return "tab decks · enter detail · / filter · M floor · : commands · s sort · S reverse · F refresh prices · v views · a add · +/- qty · d remove · u undo · q quit"
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
