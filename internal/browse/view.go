package browse

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
			v += "\n" + m.theme.Err.Render(m.confirm.prompt) +
				m.theme.Help.Render("  "+m.confirmHint())
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
	b.WriteString(m.header(left, right, maxLineWidth(rightLines)) + "\n")
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
	written := 0
	for i, line := range ui.WrapHelp(help, m.width) {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.theme.Help.Render(line))
		written++
	}
	if desc := m.paletteDesc(); desc != "" {
		b.WriteString("\n" + m.theme.Help.Render(ui.Truncate(desc, m.width)))
		written++
	}
	// Reserved-but-unused rows render blank, so the frame keeps its height
	// and the rule above the status holds still when this help is shorter
	// than the tallest one helpRows reserved for.
	for written < m.helpRows() {
		b.WriteString("\n")
		written++
	}
}

// detailView renders the card overlay in place of the two panes.
//
// It replaces the panes rather than floating over them: the oracle text and
// price history need the width, and a box drawn over a table leaves fragments
// of card names showing around its edges that read as corruption. Three
// layouts by width: wide enough (artOverflows), the art grows toward
// artColsMax and runs down the right edge beside everything — frame, HELD,
// PRICE — with the text column keeping artMinTextCols so no analysis row
// clips; anything narrower goes vertical, the art at full size between the
// card details and HELD; and a window narrower than the art itself goes
// text-only.
func (m Model) detailView() string {
	img := m.detail.image
	// artW is the width the art on screen actually occupies — the drawn
	// size, not the size the current window would ask for. Mid-resize the
	// two disagree (the re-render is in flight), and laying out against
	// the wish instead of the fact pushed lines past the terminal's edge
	// and clipped the card (observed live). The reservation for a pending
	// first fetch uses the wanted size, which is also what that fetch will
	// render at.
	artW := m.detail.imageColsDrawn
	if len(img) == 0 && m.detail.imagePending {
		// The art is coming: hold its space so HELD, PRICE and COMPS
		// render in their final positions from the first frame.
		artW = m.detailImageCols()
		img = m.blankArt(artW)
	}
	if artW <= 0 {
		artW = m.detailImageCols()
	}
	var lines []string
	// heldBase is where hoardLines' output begins within lines — the held
	// rows sit at heldBase+2+i (after the "" and "HELD" title lines) — so
	// a requested scroll-into-view can find the cursor's absolute line.
	var heldBase int
	switch {
	case len(img) > 0 && m.width-artW-2 >= artMinTextCols:
		// The art pins to the right edge and overflows beside the
		// analysis; rows past its bottom keep the text column's width,
		// which artMinTextCols guarantees is enough for every row.
		// besideImage keeps the text lines' indices, so heldBase is
		// unaffected by the art.
		textW := m.width - artW - 2
		heldBase = len(m.cardFrameLines(*m.detail, textW))
		lines = besideImage(img, m.detailLines(*m.detail, textW), textW)
	case len(img) > 0 && m.width >= artW:
		// The window can't host the art beside the analysis, so the
		// layout goes vertical: the card details, then the art at full
		// size, then HELD and the rest (owner's call — relocation over
		// shrinking the card).
		lines = append(m.cardFrameLines(*m.detail, m.width), "")
		lines = append(lines, img...)
		heldBase = len(lines)
		lines = append(lines, m.hoardLines(*m.detail, m.width)...)
	default:
		heldBase = len(m.cardFrameLines(*m.detail, m.width))
		lines = m.detailLines(*m.detail, m.width)
	}

	// A pending scroll-into-view request resolves here, where the layout
	// (and so the held cursor's absolute line) is known.
	if m.detail.scrollHeldIntoView {
		m.detail.scrollHeldIntoView = false
		if len(m.detail.holdings) > 0 {
			idx := heldBase + 2 + m.detail.heldCursor
			vis := m.visibleRows() + 1
			if idx < m.detail.scroll {
				m.detail.scroll = idx
			} else if idx >= m.detail.scroll+vis {
				m.detail.scroll = idx - vis + 1
			}
		}
	}
	// The scroll window: pgup/pgdn move it, and the clamp lives here
	// because only the render knows how many lines the overlay has.
	maxScroll := max(len(lines)-(m.visibleRows()+1), 0)
	if m.detail.scroll > maxScroll {
		m.detail.scroll = maxScroll
	}
	top := m.detail.scroll
	// The kitty transmit rides in the frame that draws its placeholders,
	// written atomically by the renderer — the one delivery the terminal
	// cannot tear. Escape sequences are zero-width, so neither the layout
	// nor the scroll math sees it; it attaches to the first rendered line,
	// which exists at any scroll position. It rides in EVERY frame while
	// the upload is dirty: the renderer drops intermediate frames, so a
	// single-frame delivery is unreliable (a crop of the previous upload,
	// observed live). The settle tick ends the embedding (onRetransmit).
	if m.detail.imageTransmit != "" && !m.detail.transmitSent &&
		len(m.detail.image) > 0 && top < len(lines) {
		lines = append([]string(nil), lines...)
		lines[top] = m.detail.imageTransmit + lines[top]
	}

	var b strings.Builder
	for i := range m.visibleRows() + 1 {
		b.WriteString(strings.TrimRight(lineAt(lines, top+i), " "))
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
		// The scroll indicator: what lies past each edge of the window,
		// and the keys that reach it.
		above, below := top, len(lines)-(top+m.visibleRows()+1)
		switch {
		case above > 0 && below > 0:
			b.WriteString(m.theme.Help.Render(fmt.Sprintf("%d lines above · %d below · pgup/pgdn scroll", above, below)))
		case below > 0:
			b.WriteString(m.theme.Help.Render(fmt.Sprintf("%d more lines · pgdn scrolls", below)))
		case above > 0:
			b.WriteString(m.theme.Help.Render(fmt.Sprintf("%d lines above · pgup scrolls", above)))
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

// helpRows is how many rows the wrapped help line costs right now,
// including the palette's description line when one is showing.
//
// While a transient surface (a confirm, a prompt) borrows the help line,
// the underlying view's help still reserves its height: the quit
// confirm's one-line help shrinking a two-line gutter grew the panes by a
// row and shifted the whole frame for the length of a y/n question
// (observed live). The unused row renders blank — space held, not moved.
func (m Model) helpRows() int {
	if !m.ready {
		return 1
	}
	rows := len(ui.WrapHelp(m.helpLine(), m.width))
	if m.confirm != nil || m.prompt != nil {
		base := m
		base.confirm, base.prompt = nil, nil
		rows = max(rows, len(ui.WrapHelp(base.helpLine(), base.width)))
	}
	// The view cycle must not bounce the gutter: a view with shorter help
	// (unpriced) reserved fewer rows than its neighbors, so v shifted the
	// rule and status up a line and back (observed live). Reserve the
	// tallest help any right-pane view would show; writeHelp pads the
	// difference blank.
	if m.palette == nil && !m.filtering && !m.watchPick && m.detail == nil && m.text == nil {
		rows = max(rows, m.tallestViewHelpRows())
	}
	if m.paletteDesc() != "" {
		rows++
	}
	return rows
}

// helpRowsKey names everything the five per-view help lines can differ on
// under helpRows' guard (no palette/filter/pick/overlay, confirm and prompt
// blanked): the wrap width, the market view's fetch state, and the holdings
// line's focus/lens/read-only variants.
type helpRowsKey struct {
	width                       int
	marketLoaded, marketLoading bool
	focus                       pane
	setsMode                    bool
	selKind                     string
}

// tallestViewHelpRows is the gutter reservation: the tallest wrapped help
// across the right-pane views. Memoized, because helpRows runs from several
// layout callers on every frame and this loop rebuilt and re-wrapped five
// views' help each time for a value that only changes on resize or the
// handful of state flips the key names. The map is a reference field, so
// the per-frame value copies of Model share one cache; a Model built
// without New (tests) has a nil map and simply recomputes.
func (m Model) tallestViewHelpRows() int {
	key := helpRowsKey{width: m.width, focus: m.focus, setsMode: m.setsMode,
		marketLoaded: m.marketLoaded, marketLoading: m.marketLoading}
	if sel := m.selectedContainer(); sel != nil {
		key.selKind = sel.Kind
	}
	if rows, ok := m.helpRowsMemo[key]; ok {
		return rows
	}
	alt := m
	alt.confirm, alt.prompt = nil, nil
	rows := 0
	for v := viewHoldings; v <= viewMarket; v++ {
		alt.view = v
		rows = max(rows, len(ui.WrapHelp(alt.helpLine(), m.width)))
	}
	if m.helpRowsMemo != nil {
		m.helpRowsMemo[key] = rows
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
func (m Model) header(left, right, tableW int) string {
	name, totals := m.viewHeader()

	totals += m.opBadge()
	// The totals hug the table's right edge, not the pane's: on a wide
	// window the table ends long before the terminal does, and counts
	// parked at the far corner read as furniture from some other surface
	// (observed live: the page phrase floating an armspan from its rows).
	anchor := right
	if tableW > 0 {
		// Never narrower than the header's own two parts. A table can be
		// narrower than its title — an empty collection's is just the column
		// names, eleven columns of "NAME  VALUE" — and anchoring to that left
		// one column for a fourteen-character title, so a first run read
		// "… 0 · $0.00" where it should have said "CARDS · BINDER". The pane's
		// width still caps it: the totals hug the table's edge where there is
		// room and the pane's where there is not.
		need := lipgloss.Width(name) + 1 + lipgloss.Width(totals)
		anchor = min(max(tableW, need), right)
	}
	title := ui.Truncate(name, max(anchor-lipgloss.Width(totals)-1, 0))
	gap := max(anchor-lipgloss.Width(title)-lipgloss.Width(totals), 0)
	leftTitle := "COLLECTION"
	if m.setsMode {
		leftTitle = "SETS"
	}
	return m.paneTitle(paneContainers).Render(fit(leftTitle, left)) +
		strings.Repeat(" ", paneGap) +
		m.paneTitle(paneCards).Render(title) + strings.Repeat(" ", gap) + m.theme.Help.Render(totals)
}

// maxLineWidth is the widest rendered content line — the right pane's true
// edge, which the header's totals align against. Styles strip before the
// trim: the cursor bar pads its row to the pane width *inside* its escape
// codes, and measuring that padding yanked the header to the far corner
// whenever the pane had focus (observed live).
func maxLineWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(strings.TrimRight(ansi.Strip(l), " ")))
	}
	return w
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

// cardLines renders the selected container's holdings. Columns pin to the
// whole filtered list's measures (Width), so >/< holds the table's shape
// instead of re-fitting to each page's longest name.
func (m Model) cardLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		inDeck := false
		if sel := m.selectedContainer(); sel != nil {
			inDeck = sel.Kind == store.KindDeck
		}

		w := m.cardsColW
		cols := []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10,
				Width: stableNameWidth(w.name, width)},
			// The identity pips sit beside the name the way the mana symbols
			// do on the card. Pure meaning-bearing ornament, so it is the
			// first column a narrow terminal gives up.
			{Title: "ID", Align: ui.Left, Priority: 8, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim(), Width: w.set},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim(), Width: w.fin},
			{Title: "QTY", Align: ui.Right, Priority: 2, Width: w.qty},
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim(), Width: w.price},
			{Title: "VALUE", Align: ui.Right, Width: w.value},
		}
		// COND is declared unconditionally and hides itself: the table drops a
		// droppable column whose every cell is a dash, so it costs nothing
		// until something is actually assessed. It has to be *declared*
		// regardless, because since schema v23 a card held in two conditions is
		// two rows, and without the column they read as one row printed twice.
		cols = slices.Insert(cols, 4,
			ui.Col{Title: "COND", Align: ui.Left, Priority: 3, Style: env.Dim()})
		if inDeck {
			// Board only means something inside a deck; against loose holdings
			// it would be a column of blanks. It follows the name block (name
			// and pips) that the eye reads first, and qualifies it.
			cols = slices.Insert(cols, 2,
				ui.Col{Title: "BOARD", Align: ui.Left, Priority: 7, Style: env.Dim()})
		}
		t := ui.Table{Cols: cols}

		for _, c := range m.cards {
			finish := ui.FinishTreated(c.Finish, c.Treatment)
			cells := []ui.Cell{
				{Text: c.Name, Style: env.Identity(c.ColorIdentity)},
				ui.C(ui.Pips(c.ColorIdentity)),
				ui.C(ui.Printing(c.SetCode, c.CollectorNumber)), ui.C(finish),
				ui.C(ui.Qty(c.Quantity)), ui.C(ui.MoneyPtr(c.Price)),
				ui.C(ui.Money(c.Value)),
			}
			// Inserted in the same order the columns were, so the two edits
			// cannot drift: COND at 4 first, then BOARD at 2 shifts it along.
			cells = slices.Insert(cells, 4, ui.C(ui.Condition(c.Condition)))
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
		case i == m.cursor[p] && (p == paneContainers || (m.view == viewHoldings && i > 0)):
			// The unfocused pane keeps a mark on its row so switching back is
			// not a hunt for where the cursor was. Not on the analytical
			// views' rows, and not on holdings row zero: moving the container
			// cursor resets the card cursor to the top, so a mark there is
			// the default position wearing a highlight — it read as a
			// selected first card in a pane the user never tabbed into. A
			// remembered place deeper in the list still shows.
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
		return m.theme.Err.Render(m.confirm.prompt) + m.theme.Help.Render("  "+m.confirmHint())
	case modePrompt:
		return m.promptLine()
	case modePalette:
		return ": " + m.palette.Query + "▏"
	case modeFilter:
		bar := "/" + m.filterText + "▏"
		if m.filterErr != "" {
			return bar + "  " + m.theme.Err.Render(m.filterErr)
		}
		// The whole filtered result, not the page on screen — "50 match"
		// on every page of a 300-row result would read as the answer.
		return bar + m.theme.Help.Render(fmt.Sprintf("  %d match", len(m.filteredCards)))
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
					"no price movement in %s this window · All Cards shows every container", sel.Name))
			}
			return m.theme.Help.Render(
				"no price movement in this window · F fetches prices and 90 days of history · W widens the window")
		case viewWatches:
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no watches shown for %s · All Cards shows every container", sel.Name))
			}
			return m.theme.Help.Render(
				"no watches · press w on a card in holdings, or : then AddWatchFromCollection")
		case viewUnpriced:
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no unpriced cards in %s · All Cards shows every container", sel.Name))
			}
			return m.theme.Help.Render("every card you own has a price")
		}
		return m.theme.Help.Render("nothing here")
	}
	// Position leads, then the selection: x/y sits in a fixed spot at the
	// line's left edge, so it can be read at a glance mid-scroll while the
	// name beside it varies in length.
	pos := fmt.Sprintf("%d/%d", m.cursor[m.focus]+1, n)
	if name := m.selectedItemName(); name != "" {
		pos += " · " + name
	}
	pos += " · sorted by " + m.sortLabel()
	if !m.filter.empty() {
		pos += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.raw)
	}
	if min := m.floorMin(); min > 0 {
		pos += fmt.Sprintf(" · floor %s (M cycles)", ui.Money(min))
	}
	// The default noise gate announces itself: rows silently absent read
	// as missing data, and the palette command that shows them is only
	// discoverable if something points at it.
	if m.view == viewMovers && !m.moversPennies {
		pos += fmt.Sprintf(" · penny filter ≤ %s (: toggles)", ui.Money(m.moversPennyLimit))
	}
	return m.theme.Help.Render(pos)
}

// selectedItemName names the row under the cursor in the focused pane —
// the card on the right, the binder, deck, or set on the left — empty
// when nothing is selectable.
func (m Model) selectedItemName() string {
	if m.focus == paneContainers {
		if sel := m.selectedContainer(); sel != nil {
			return sel.Name
		}
		return ""
	}
	i := m.cursor[paneCards]
	name := func(n int, get func(int) string) string {
		if i < 0 || i >= n {
			return ""
		}
		return get(i)
	}
	switch m.view {
	case viewMovers:
		return name(len(m.movers), func(i int) string { return m.movers[i].Name })
	case viewUnpriced:
		return name(len(m.unpriced), func(i int) string { return m.unpriced[i].Name })
	case viewWatches:
		return name(len(m.watches), func(i int) string { return m.watches[i].Name })
	case viewMarket:
		if c := m.selectedComp(); c != nil {
			return c.Card.Name
		}
		return name(len(m.marketRows), func(i int) string { return m.marketRows[i].Card.Name })
	}
	return name(len(m.cards), func(i int) string { return m.cards[i].Name })
}

// confirmHint is the keys a staged confirm answers to, in the slot beside
// its prompt — which is the only place they appear. A question and its
// answer belong on one line, and on the add-cascade takeover, where no help
// line renders at all, this is the sole carrier.
//
// The question's own wording when it has one: "y/n" alone left readers
// guessing whether n was required or any key would do.
func (m Model) confirmHint() string {
	if m.confirm != nil && m.confirm.help != "" {
		return m.confirm.help
	}
	return "y/n"
}

// helpLine is the curated prose under the frame: the handful of keys worth
// naming on this view, in the order they are worth naming.
//
// Composed from entries rather than written as a string, so the palette hint
// is one shared value instead of eleven literals that have to agree — see
// ui.HelpCommands. Every line that offers the palette leads with it.
func (m Model) helpLine() string {
	// The keys shared by every table view, in the order they trail a line.
	const quit = "q"
	tail := func(extra ...ui.HelpEntry) []ui.HelpEntry {
		return append(extra, ui.K(quit, "quit"))
	}

	switch {
	case m.confirm != nil:
		// Nothing. The question above already carries its keys — see
		// confirmHint — and repeating them here printed the same sentence
		// twice, one line under the other. A staged question answers to
		// exactly the keys it names, so there is no second set to list.
		return ""
	case m.prompt != nil:
		if m.prompt.help != "" {
			return m.prompt.help
		}
		// No "type the answer": the cursor is already in the field and the
		// question is on screen above it, so the only thing left to say is
		// what the keys do.
		return ui.Help(ui.K("enter", "accept"), ui.K("esc", "cancel"),
			ui.K("ctrl+u", "wipe"))
	case m.palette != nil:
		return ui.PaletteHelp
	case m.filtering && m.watchPick:
		return ui.Help(ui.Say("type to find the card"), ui.K("↑/↓", "move"),
			ui.K("enter", "watch it"), ui.K("tab", "decks/binders"), ui.K("esc", "cancel"))
	case m.filtering:
		return ui.Help(ui.Say("type to filter"), ui.K("enter", "keep"),
			ui.K("esc", "clear"), ui.K("ctrl+u", "wipe"), ui.K("↑/↓", "move"))
	case m.watchPick:
		return ui.Help(ui.K("↑/↓", "pick the card"), ui.K("enter", "watch it"),
			ui.K("tab", "decks/binders"), ui.K("/", "filter"), ui.K("esc", "cancel"))
	case m.detail != nil:
		// The overlay is a reading surface with its own zones, so its line is
		// assembled from what the cursor is actually on.
		if m.detail.zone == zoneHeld {
			return ui.Help(ui.K("↑/↓", "held rows"), ui.K("←/→", "field"),
				ui.K("enter", "edit"), ui.K("+/-", "qty"), ui.K("d", "remove"),
				ui.K("tab", "links"), ui.K("esc", "back"), ui.K(quit, "quit"))
		}
		var e []ui.HelpEntry
		if len(m.detail.holdings) > 0 {
			e = append(e, ui.K("tab", "held list"))
		}
		// The edit keys show only when the row under the held cursor can
		// take them — a deck row would answer every one with a refusal.
		if d := m.detail; len(d.holdings) > 0 {
			h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
			if h.ContainerKind == store.KindCollection {
				e = append(e, ui.K("+/-", "qty"), ui.K("d", "remove"))
			}
		}
		if m.openURL != nil && len(m.detail.links) > 0 {
			e = append(e, ui.K("←/→", "links"), ui.K("enter", "open in browser"))
		}
		return ui.Help(append(e, ui.K("esc", "back"), ui.K(quit, "quit"))...)
	case m.text != nil:
		return ui.Help(ui.K("↑/↓", "scroll"), ui.K("pgup/pgdn", "page"),
			ui.K("g/G", "ends"), ui.K("esc", "back"), ui.K("ctrl+c", "force quit"))
	case m.view == viewMarket && !m.marketLoaded && !m.marketLoading:
		return ui.Help(ui.K("F", "fetch vendor prices"), ui.K("v", "next view"), ui.K(quit, "quit"))
	case m.view == viewMarket && m.marketLoading:
		return ui.Help(ui.K("esc", "cancel"), ui.K("ctrl+c", "force quit"))
	case m.view == viewMarket:
		return ui.Help(tail(ui.HelpCommands, ui.K("enter", "detail"),
			ui.K("]/[", "next/prev table"), ui.K(">/<", "turn page"),
			ui.K("b", "table side/band"), ui.K("F", "refetch quotes"),
			ui.K("M", "floor"), ui.K("tab", "collections"), ui.K("v", "next view"),
			ui.K("↑/↓", "move"))...)
	case m.view == viewWatches:
		// This line once read ": add a watch", because a generic label hid
		// that watches can be added at all — the two commands that add one
		// from here carry no key, so the palette is their only route. The
		// label is uniform now for the same reason every other view's is;
		// what recovers the discovery is that the palette ranks the watch
		// verbs to the top of this view (see the watch commands' rank).
		return ui.Help(tail(ui.HelpCommands, ui.K("w", "edit threshold"),
			ui.K("d", "remove"), ui.K("enter", "detail"), ui.K("M", "floor"),
			ui.K("tab", "collections"), ui.K("v", "next view"), ui.K("↑/↓", "move"))...)
	case m.view == viewMovers:
		return ui.Help(tail(ui.HelpCommands, ui.K("W", "lookback 7/30/90 days"),
			ui.K("F", "update prices + history"), ui.K("enter", "detail"),
			ui.K(">/<", "page"), ui.K("M", "floor"), ui.K("tab", "collections"),
			ui.K("v", "next view"), ui.K("↑/↓", "move"), ui.K("s", "sort"))...)
	case m.view == viewUnpriced:
		return ui.Help(tail(ui.HelpCommands, ui.K("F", "refresh prices"),
			ui.K("enter", "detail"), ui.K("tab", "collections"), ui.K("v", "next view"),
			ui.K("↑/↓", "move"), ui.K("s", "sort"))...)
	case m.view != viewHoldings:
		// The editing keys do not apply to an analytical view, so offering
		// them here would be an invitation to a refusal.
		return ui.Help(tail(ui.HelpCommands, ui.K("tab", "collections"),
			ui.K("v", "next view"), ui.K("F", "fetch data"), ui.K("↑/↓", "move"),
			ui.K("s", "sort"), ui.K("S", "reverse"))...)
	case m.focus == paneContainers:
		// The sets pane is a read-only lens: no create/rename/remove verbs,
		// just the toggle back.
		if m.setsMode {
			return ui.Help(tail(ui.HelpCommands, ui.K("tab", "cards"),
				ui.K("B", "binders/decks"), ui.K("a", "add cards"), ui.K("/", "filter"),
				ui.K("M", "floor"), ui.K("F", "refresh prices"), ui.K("v", "views"))...)
		}
		e := []ui.HelpEntry{ui.HelpCommands, ui.K("tab", "cards"), ui.K("B", "by set"),
			ui.K("n", "new binder"), ui.K("a", "add cards")}
		// The merged all-cards row is read-only, so its help drops the
		// verbs that would only ever answer with a refusal.
		if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
			e = append(e, ui.K("R", "rename"), ui.K("d", "remove"))
		}
		e = append(e, ui.K("/", "filter"), ui.K("M", "floor"),
			ui.K("F", "refresh prices"), ui.K("v", "views"))
		if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
			e = append(e, ui.K("u", "undo"))
		}
		return ui.Help(tail(e...)...)
	}

	// The cards pane. Set rows take the edit verbs: they resolve to the
	// binders holding the printing. +/- is offered unconditionally — with
	// several binders in play it answers by naming them, which is the more
	// useful reply than a key that looks unavailable.
	sel := m.selectedContainer()
	lens := "decks"
	if sel != nil && sel.Kind == kindSet {
		lens = "sets"
	}
	e := []ui.HelpEntry{ui.HelpCommands, ui.K("tab", lens), ui.K("enter", "detail"),
		ui.K(">/<", "page"), ui.K("/", "filter"), ui.K("M", "floor"),
		ui.K("s", "sort"), ui.K("S", "reverse"), ui.K("F", "refresh prices"),
		ui.K("v", "views"), ui.K("a", "add")}
	// The merged all-cards row is read-only.
	if sel == nil || sel.Kind != kindAllCards {
		e = append(e, ui.K("+/-", "qty"), ui.K("d", "remove"), ui.K("u", "undo"))
	}
	return ui.Help(tail(e...)...)
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
