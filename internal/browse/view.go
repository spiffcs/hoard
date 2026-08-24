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

const (
	containerPaneWidth = 30
	paneGap            = 2
	minCardPaneWidth   = 24

	chromeRows = 5
)

func (m Model) View() string {
	if !m.ready {

		return ""
	}

	if m.addChild != nil {

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
	leftCol := m.leftColumn(left, m.visibleRows())
	rightLines := m.rightLines(right)

	var b strings.Builder
	b.WriteString(m.header(left, right, maxLineWidth(rightLines)) + "\n")
	for i := range m.visibleRows() {
		b.WriteString(leftCol[i])
		b.WriteString(strings.Repeat(" ", paneGap))

		b.WriteString(strings.TrimRight(fit(lineAt(rightLines, i), right), " "))
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	b.WriteString(m.statusLine() + "\n")

	if m.palette != nil {
		for _, line := range m.paletteLines(m.width) {
			b.WriteString(line + "\n")
		}
	}
	m.writeHelp(&b, m.helpLine())
	return b.String()
}

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

	for written < m.helpRows() {
		b.WriteString("\n")
		written++
	}
}

func (m Model) detailView() string {
	img := m.detail.image

	artW := m.detail.imageColsDrawn
	if len(img) == 0 && m.detail.imagePending {

		artW = m.detailImageCols()
		img = m.blankArt(artW)
	}
	if artW <= 0 {
		artW = m.detailImageCols()
	}
	var lines []string

	var heldBase int
	switch {
	case len(img) > 0 && m.width-artW-2 >= artMinTextCols:

		textW := m.width - artW - 2
		heldBase = len(m.cardFrameLines(*m.detail, textW))
		lines = besideImage(img, m.detailLines(*m.detail, textW), textW)
	case len(img) > 0 && m.width >= artW:

		lines = append(m.cardFrameLines(*m.detail, m.width), "")
		lines = append(lines, img...)
		heldBase = len(lines)
		lines = append(lines, m.hoardLines(*m.detail, m.width)...)
	default:
		heldBase = len(m.cardFrameLines(*m.detail, m.width))
		lines = m.detailLines(*m.detail, m.width)
	}

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

	maxScroll := max(len(lines)-(m.visibleRows()+1), 0)
	if m.detail.scroll > maxScroll {
		m.detail.scroll = maxScroll
	}
	top := m.detail.scroll

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

	switch {
	case m.confirm != nil || m.prompt != nil || m.palette != nil:
		b.WriteString(m.statusLine())
	case m.op != nil || m.status != "":
		b.WriteString(m.statusLine())
	default:

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

func (m Model) paneWidths() (left, right int) {
	left = containerPaneWidth
	if m.width-left-paneGap < minCardPaneWidth {
		left = max(m.width-paneGap-minCardPaneWidth, 0)
	}
	right = max(m.width-left-paneGap, 0)
	return left, right
}

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

	if m.palette == nil && !m.filtering && !m.watchPick && m.detail == nil && m.text == nil {
		rows = max(rows, m.tallestViewHelpRows())
	}
	if m.paletteDesc() != "" {
		rows++
	}
	return rows
}

type helpRowsKey struct {
	width                       int
	marketLoaded, marketLoading bool
	focus                       pane
	setsMode                    bool
	selKind                     string
	settling                    bool
}

func (m Model) helpRowsKeyFor() helpRowsKey {
	key := helpRowsKey{width: m.width, focus: m.focus, setsMode: m.setsMode,
		marketLoaded: m.marketLoaded, marketLoading: m.marketLoading,
		settling: m.anySettling(m.now())}
	if sel := m.selectedContainer(); sel != nil {
		key.selKind = sel.Kind
	}
	return key
}

func (m Model) tallestViewHelpRows() int {
	key := m.helpRowsKeyFor()
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

func (m Model) visibleRows() int {
	return max(m.height-chromeRows-m.paletteRows()-(m.helpRows()-1), 1)
}

func sectionBudgets(counts []int, pool, cursorSec int) []int {
	budget := make([]int, len(counts))
	var active []int
	for i, n := range counts {
		if n > 0 {
			active = append(active, i)
		}
	}
	for len(active) > 0 {
		share := pool / len(active)
		if share == 0 {
			break
		}
		kept := active[:0]
		satisfied := false
		for _, i := range active {
			if counts[i] <= share {
				budget[i] = counts[i]
				pool -= counts[i]
				satisfied = true
				continue
			}
			kept = append(kept, i)
		}
		active = kept
		if satisfied {
			continue
		}

		for _, i := range active {
			budget[i] = share
			pool -= share
		}
		for _, i := range active {
			if pool == 0 {
				break
			}
			if budget[i] < counts[i] {
				budget[i]++
				pool--
			}
		}
		break
	}
	if cursorSec >= 0 && cursorSec < len(budget) &&
		budget[cursorSec] == 0 && counts[cursorSec] > 0 {
		big := 0
		for i := range budget {
			if budget[i] > budget[big] {
				big = i
			}
		}
		if budget[big] > 0 {
			budget[big]--
			budget[cursorSec] = 1
		}
	}
	return budget
}

func (m *Model) scrollIntoView() {
	for p := range m.offset {
		rows := max(m.paneRows(pane(p))-1, 1)
		if pane(p) == paneCards && m.view == viewMarket {

			m.scrollMarketIntoView()
			continue
		}
		if pane(p) == paneCards && m.view == viewWatches {

			m.scrollWatchesIntoView()
			continue
		}
		if pane(p) == paneCards && m.view == viewDip {

			m.scrollDipIntoView()
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

func (m Model) header(left, right, tableW int) string {
	name, totals := m.viewHeader()

	totals += m.opBadge()

	anchor := right
	if tableW > 0 {

		need := lipgloss.Width(name) + 1 + lipgloss.Width(totals)
		anchor = min(max(tableW, need), right)
	}
	title := ui.Truncate(name, max(anchor-lipgloss.Width(totals)-1, 0))
	gap := max(anchor-lipgloss.Width(title)-lipgloss.Width(totals), 0)
	leftTitle := "COLLECTION"
	if m.setsMode {
		leftTitle = "SETS"
	}
	leftHead := m.paneTitle(paneContainers).Render(leftTitle)
	if note := m.containerScrollNote(); note != "" {
		leftHead += m.theme.Help.Render(note)
	}
	return fit(leftHead, left) +
		strings.Repeat(" ", paneGap) +
		m.paneTitle(paneCards).Render(title) + strings.Repeat(" ", gap) + ui.Restyle(totals, m.theme.Help)
}

func maxLineWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(strings.TrimRight(ansi.Strip(l), " ")))
	}
	return w
}

func (m Model) paneTitle(p pane) lipgloss.Style {
	if m.focus == p {
		return m.theme.Accent
	}
	return m.theme.Title
}

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

func (m Model) containerLines(width int) []string {
	now := m.now()
	return m.paneLines(paneContainers, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "", Align: ui.Left, Priority: 1},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8,
				Width: stableNameWidth(m.containerNameW, width)},
			{Title: "VALUE", Align: ui.Right},
		}}
		for i, c := range m.containers {
			mark := ""
			if c.settling(now) || c.skipped() {
				mark = settlingMark
			}
			if c.Kind == kindFolder {
				mark = foldOpenMark
				if m.collapsed[c.ID] {
					mark = foldShutMark
				}
			}
			gutter, indent := mark, strings.Repeat("  ", c.depth)
			if c.depth > 0 && mark != "" {
				gutter, indent = "", strings.Repeat("  ", c.depth-1)+mark+" "
			}
			name := indent + c.Name
			if !m.containerEligible(i) || c.skipped() {
				t.AddStyled(env.Dim(), ui.C(gutter), ui.C(name), ui.C(ui.Money(c.Value)))
				continue
			}
			t.Add(ui.C(gutter), ui.C(name), ui.C(ui.Money(c.Value)))
		}
		return t
	})
}

func (m Model) rightLines(width int) []string {
	switch m.view {
	case viewWatches:
		return m.watchesLines(width)
	case viewMovers:
		return m.moversLines(width)
	case viewMarket:
		return m.marketLines(width)
	case viewDip:
		return m.dipLines(width)
	}
	return m.cardLines(width)
}

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

			{Title: "ID", Align: ui.Left, Priority: 8, Style: env.PipsStyle()},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim(), Width: w.set},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim(), Width: w.fin},
			{Title: "QTY", Align: ui.Right, Priority: 2, Width: w.qty},
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim(), Width: w.price},
			{Title: "VALUE", Align: ui.Right, Width: w.value},
		}

		cols = slices.Insert(cols, 4,
			ui.Col{Title: "COND", Align: ui.Left, Priority: 3, Style: env.Dim()})
		if m.setUnowned {
			cols = slices.Insert(cols, 5,
				ui.Col{Title: "LIST", Align: ui.Left, Priority: 1, Style: env.Dim()})
		}
		if inDeck {

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

			cells = slices.Insert(cells, 4, ui.C(ui.Condition(c.Condition)))
			if m.setUnowned {
				cells = slices.Insert(cells, 5, ui.C(c.Where))
			}
			if inDeck {
				cells = slices.Insert(cells, 2, ui.C(c.Board))
			}
			t.Add(cells...)
		}
		return t
	})
}

func (m Model) window(lines []string, p pane, width int) []string {
	if len(lines) == 0 {
		return nil
	}
	body := lines[1:]
	budget := m.paneRows(p)
	start := min(m.offset[p], max(len(body)-1, 0))
	end := min(start+budget-1, len(body))

	out := make([]string, 0, budget)
	out = append(out, lines[0])
	for i := start; i < end; i++ {
		line := fit(body[i], width)
		switch {
		case i == m.cursor[p] && m.focus == p:

			line = ui.Restyle(line, m.theme.Cursor)
		case p == paneCards && m.selectedRow(i):

			line = ui.Restyle(line, m.theme.Cursor)
		case i == m.cursor[p] && (p == paneContainers || (m.view == viewHoldings && i > 0)):

			line = ui.Restyle(line, m.theme.Inactive)
		}
		out = append(out, line)
	}
	return out
}

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

		if note := m.filterUnsupported(); note != "" {
			return bar + "  " + m.theme.Err.Render(note)
		}

		if n := m.filterMatchCount(); n >= 0 {
			return bar + m.theme.Help.Render(fmt.Sprintf("  %d match", n))
		}
		return bar
	}
	if m.status != "" {
		if m.statusErr {
			return m.theme.Err.Render(m.status)
		}
		return m.status
	}

	if m.op != nil {
		return m.opStatus()
	}
	if m.view == viewMarket {
		return m.marketStatus()
	}
	if m.view == viewWatches {

		return m.watchStatus()
	}
	if m.emptyNote != "" {
		return m.theme.Help.Render(m.emptyNote)
	}

	n := m.rowCount(m.focus)
	if n == 0 {

		sel := m.selectedContainer()
		scoped := sel != nil && sel.Kind != kindAllCards && m.view != viewHoldings
		switch m.view {
		case viewMovers:

			if !m.filter.Empty() {
				return m.theme.Help.Render(fmt.Sprintf(
					"no movers match %s · esc clears the filter", m.filter.Raw()))
			}
			if scoped {
				return m.theme.Help.Render(fmt.Sprintf(
					"no price movement in %s this window · All Cards shows every container", sel.Name))
			}
			return m.theme.Help.Render(
				"no price movement in this window · F fetches prices and 90 days of history · W widens the window")
		}
		return m.theme.Help.Render("nothing here")
	}

	pos := fmt.Sprintf("%d/%d", m.cursor[m.focus]+1, n)
	if name := m.selectedItemName(); name != "" {
		pos += " · " + name
	}
	pos += " · sorted by " + m.sortLabel()
	if !m.filter.Empty() {
		pos += fmt.Sprintf(" · filtered by %s (esc to clear)", m.filter.Raw())
	}
	if min := m.floorMin(); min > 0 {
		pos += fmt.Sprintf(" · floor %s (M cycles)", ui.Money(min))
	}

	if m.view == viewMovers && !m.moversPennies {
		pos += fmt.Sprintf(" · penny filter ≤ %s (: toggles)", ui.Money(m.moversPennyLimit))
	}
	return m.theme.Help.Render(pos)
}

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
	case viewWatches:

		return m.selectedWatchName()
	case viewMarket:
		if c := m.selectedComp(); c != nil {
			return c.Card.Name
		}
		return name(len(m.marketRows), func(i int) string { return m.marketRows[i].Card.Name })
	}
	return name(len(m.cards), func(i int) string { return m.cards[i].Name })
}

func (m Model) confirmHint() string {
	if m.confirm != nil && m.confirm.help != "" {
		return m.confirm.help
	}
	return "y/n"
}

func (m Model) helpLine() string {

	const quit = "q"
	tail := func(extra ...ui.HelpEntry) []ui.HelpEntry {
		return append(extra, ui.K(quit, "quit"))
	}

	switch {
	case m.confirm != nil:

		return ""
	case m.prompt != nil:
		if m.prompt.help != "" {
			return m.prompt.help
		}

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

		if m.detail.zone == zoneHeld {
			return ui.Help(ui.K("↑/↓", "held rows"), ui.K("←/→", "field"),
				ui.K("enter", "edit"), ui.K("+/-", "qty"), ui.K("d", "remove"),
				ui.K("tab", "next field"), ui.K("esc", "back"), ui.K(quit, "quit"))
		}
		var e []ui.HelpEntry
		if len(m.detail.holdings) > 0 {
			e = append(e, ui.K("tab", "held list"))
		}

		if d := m.detail; len(d.holdings) > 0 {
			h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
			if editableKind(h.ContainerKind) {
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

		return ui.Help(tail(ui.HelpCommands, ui.K("w", "edit threshold"),
			ui.K("d", "remove"), ui.K("enter", "detail"),
			ui.K("]/[", "next/prev table"), ui.K("/", "filter"), ui.K("M", "floor"),
			ui.K("F", "refresh prices"), ui.K("tab", "collections"),
			ui.K("v", "next view"), ui.K("↑/↓", "move"), ui.K("s", "sort"))...)
	case m.view == viewDip:
		return ui.Help(tail(ui.HelpCommands, ui.K("W", "lookback 7/30/90 days"),
			ui.K("enter", "detail"), ui.K("M", "floor"), ui.K("/", "filter"),
			ui.K("tab", "collections"), ui.K("v", "next view"), ui.K("↑/↓", "move"))...)
	case m.view == viewMovers:
		return ui.Help(tail(ui.HelpCommands, ui.K("W", "lookback 7/30/90 days"),
			ui.K("F", "update prices + history"), ui.K("enter", "detail"),
			ui.K(">/<", "page"), ui.K("M", "floor"), ui.K("tab", "collections"),
			ui.K("v", "next view"), ui.K("↑/↓", "move"), ui.K("s", "sort"))...)
	case m.view != viewHoldings:

		return ui.Help(tail(ui.HelpCommands, ui.K("tab", "collections"),
			ui.K("v", "next view"), ui.K("F", "fetch data"), ui.K("↑/↓", "move"),
			ui.K("s", "sort"), ui.K("S", "reverse"))...)
	case m.focus == paneContainers:

		if m.setsMode {
			e := []ui.HelpEntry{ui.HelpCommands, ui.K("tab", "cards"),
				ui.K("B", "binders/decks"), ui.K("a", "add cards"), ui.K("/", "filter"),
				ui.K("M", "floor"), ui.K("F", "refresh prices"), ui.K("v", "views")}

			if m.anySettling(m.now()) {
				e = append(e, ui.Say(settlingMark+" new set: held out of movers net"))
			}
			return ui.Help(tail(e...)...)
		}
		e := []ui.HelpEntry{ui.HelpCommands, ui.K("tab", "cards"), ui.K("B", "by set"),
			ui.K("n", "new binder"), ui.K("a", "add cards")}

		if sel := m.selectedContainer(); sel != nil && sel.Kind == store.KindDeck {
			e = append(e, ui.K("m", "move to folder"))
		}
		if m.foldTarget() != nil {
			e = append(e, ui.K("space", "fold"))
		}

		if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
			e = append(e, ui.K("R", "rename"), ui.K("d", "remove"))
		}
		if sel := m.selectedContainer(); sel != nil && holdsCards(*sel) {

			label := "exclude"
			if !sel.Counted {
				label = "include"
			}
			e = append(e, ui.K("x", label))
		}
		e = append(e, ui.K("/", "filter"), ui.K("M", "floor"),
			ui.K("F", "refresh prices"), ui.K("v", "views"))
		if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
			e = append(e, ui.K("u", "undo"))
		}
		return ui.Help(tail(e...)...)
	}

	sel := m.selectedContainer()
	lens := "decks"
	if sel != nil && sel.Kind == kindSet {
		lens = "sets"
	}
	e := []ui.HelpEntry{ui.HelpCommands, ui.K("tab", lens), ui.K("enter", "detail"),
		ui.K("shift+↑/↓", "select"),
		ui.K(">/<", "page"), ui.K("/", "filter"), ui.K("M", "floor"),
		ui.K("s", "sort"), ui.K("S", "reverse"), ui.K("F", "refresh prices"),
		ui.K("v", "views"), ui.K("a", "add")}

	if sel == nil || sel.Kind != kindAllCards {
		e = append(e, ui.K("+/-", "qty"), ui.K("d", "remove"), ui.K("u", "undo"))
	}
	return ui.Help(tail(e...)...)
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

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
