package browse

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const sparkCells = 32

type detail struct {
	card     store.CardDetail
	holdings []store.Holding
	series   map[finish.Finish][]store.PricePoint
	bids     map[finish.Finish][]store.PricePoint

	comps        map[finish.Finish]market.Comp
	compsOK      bool
	compsPending bool

	links      []cardLink
	linkCursor int

	heldCursor int

	fromArbitrage bool

	scroll int

	scrollHeldIntoView bool

	zone int

	heldField int

	imagePending bool

	imageColsDrawn int

	imageTransmit string
	transmitSent  bool

	image []string
}

const (
	zoneLinks = iota
	zoneHeld
)

const (
	fieldQty = iota
	fieldSet
	fieldFinish
	fieldCondition
	fieldWhere
	heldFieldCount
)

func (m *Model) openDetail() tea.Cmd {
	id, fromArbitrage := m.selectedCardID(), m.fromArbitrageRow()
	if id == "" {
		return nil
	}
	d := detail{fromArbitrage: fromArbitrage}
	if !m.loadPrinting(&d, id) {
		return nil
	}

	var err error
	if d.holdings, err = m.store.HoldingsOfName(d.card.Name); err != nil {
		m.setError(err)
		return nil
	}
	if len(d.holdings) == 0 {

		if d.holdings, err = m.store.HoldingsOf(id); err != nil {
			m.setError(err)
			return nil
		}
	}
	for i, h := range d.holdings {
		if h.ScryfallID == id {
			d.heldCursor = i
			break
		}
	}
	m.refreshLinks(&d)
	m.detail = &d
	return m.fetchDetailComps(id)
}

func (m *Model) reloadDetail() tea.Cmd {
	d := m.detail
	if d == nil {
		return nil
	}
	if !m.loadPrinting(d, d.card.ScryfallID) {
		return nil
	}
	holdings, err := m.store.HoldingsOfName(d.card.Name)
	if err != nil {
		m.setError(err)
		return nil
	}
	if len(holdings) > 0 {
		d.holdings = holdings
	}
	d.heldCursor = min(max(d.heldCursor, 0), max(len(d.holdings)-1, 0))
	m.refreshLinks(d)
	return m.fetchDetailComps(d.card.ScryfallID)
}

func (m *Model) closeDetailIfUnheld() {
	d := m.detail
	if d == nil {
		return
	}
	holdings, err := m.store.HoldingsOfName(d.card.Name)
	if err != nil || len(holdings) > 0 {
		return
	}
	m.detail, m.detailComps = nil, nil
}

func (m *Model) refreshLinks(d *detail) {
	if m.openURL == nil {
		return
	}
	cur := d.linkCursor
	d.links = cardLinks(d.card, heldFoil(d))
	d.linkCursor = min(max(cur, 0), max(len(d.links)-1, 0))
}

func heldFoil(d *detail) bool {
	if len(d.holdings) == 0 {
		return false
	}
	h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
	return h.Finish.UsesFoilPricing()
}

func (m *Model) loadPrinting(d *detail, id string) bool {
	d.series = map[finish.Finish][]store.PricePoint{}
	d.bids = map[finish.Finish][]store.PricePoint{}

	var err error
	if d.card, err = m.store.CardDetail(id); err != nil {
		m.setError(err)
		return false
	}

	for _, fin := range []finish.Finish{finish.Nonfoil, finish.Foil} {
		s, err := m.store.PriceSeries(id, fin)
		if err != nil {
			m.setError(err)
			return false
		}
		if len(s) > 0 {
			d.series[fin] = s
		}
		b, err := m.store.BidSeries(id, fin)
		if err != nil {
			m.setError(err)
			return false
		}
		if len(b) > 0 {
			d.bids[fin] = b
		}
	}

	if m.cardComps != nil {
		if r, ok := m.detailComps[id]; ok {
			d.comps, d.compsOK, d.compsPending = r.comps, r.ok, false
		} else {
			d.comps, d.compsOK, d.compsPending = nil, false, true
		}
	}
	return true
}

type compsResult struct {
	comps map[finish.Finish]market.Comp
	ok    bool
}

type detailCompsMsg struct {
	scryfallID string
	comps      map[finish.Finish]market.Comp
	ok         bool
}

func (m *Model) fetchDetailComps(id string) tea.Cmd {
	if m.cardComps == nil || id == "" {
		return nil
	}
	if _, ok := m.detailComps[id]; ok {
		return nil
	}
	f := m.cardComps
	return func() tea.Msg {
		comps, ok := f(id)
		return detailCompsMsg{scryfallID: id, comps: comps, ok: ok}
	}
}

func (m Model) onDetailComps(msg detailCompsMsg) (tea.Model, tea.Cmd) {
	if m.detailComps == nil {
		m.detailComps = map[string]compsResult{}
	}
	m.detailComps[msg.scryfallID] = compsResult{comps: msg.comps, ok: msg.ok}
	if d := m.detail; d != nil && d.card.ScryfallID == msg.scryfallID {
		d.comps, d.compsOK, d.compsPending = msg.comps, msg.ok, false
	}
	return m, nil
}

func (m *Model) moveHeldCursor(delta int) tea.Cmd {
	d := m.detail
	if d == nil || len(d.holdings) == 0 {
		return nil
	}
	next := min(max(d.heldCursor+delta, 0), len(d.holdings)-1)
	if next == d.heldCursor {
		return nil
	}
	d.heldCursor = next
	d.scrollHeldIntoView = true
	var cmds []tea.Cmd
	if sel := d.holdings[next]; sel.ScryfallID != "" && sel.ScryfallID != d.card.ScryfallID {
		if !m.loadPrinting(d, sel.ScryfallID) {
			return nil
		}
		img := m.fetchDetailImage()
		if img == nil {

			d.image = nil
		}
		cmds = append(cmds, img, m.fetchDetailComps(sel.ScryfallID))
	}
	m.refreshLinks(d)
	return tea.Batch(cmds...)
}

type cardLink struct {
	name string
	url  string
}

func cardLinks(c store.CardDetail, foil bool) []cardLink {
	q := url.QueryEscape(c.Name)
	tcg := "https://www.tcgplayer.com/search/magic/product?q=" + q
	switch {
	case foil && c.Treatment != "":

		tcg = "https://www.tcgplayer.com/search/magic/product?q=" +
			url.QueryEscape(c.Name+" "+c.Treatment+" foil")
	case c.TCGplayerID != nil:
		tcg = fmt.Sprintf("https://www.tcgplayer.com/product/%d", *c.TCGplayerID)
	}

	ck := "https://www.cardkingdom.com/catalog/search?search=header&filter%5Bname%5D=" + q
	switch {
	case foil && c.CKFoilURL != "":
		ck = c.CKFoilURL
	case c.CKURL != "":
		ck = c.CKURL
	}
	links := []cardLink{
		{"tcgplayer.com", tcg},
		{"manapool.com", fmt.Sprintf("https://manapool.com/card/%s/%s/%s",
			c.SetCode, c.CollectorNumber, nameSlug(c.Name))},
		{"cardkingdom.com", ck},
	}
	if c.ScryfallURL != "" {
		links = append(links, cardLink{"scryfall.com", c.ScryfallURL})
	}
	return links
}

func finishLabel(fin finish.Finish, treatment string) string {
	if fin == finish.Foil && treatment != "" {
		return treatment
	}
	if fin == finish.Nonfoil {
		return "non-foil"
	}
	return fin.String()
}

func nameSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

const frameWidth = 66

func (m Model) detailLines(d detail, width int) []string {
	return append(m.cardFrameLines(d, width), m.hoardLines(d, width)...)
}

func (m Model) cardFrameLines(d detail, width int) []string {
	dim := m.theme.Help.Render
	var out []string

	c := d.card
	cardW := min(width, frameWidth)

	name := m.theme.Identity(c.ColorIdentity).Bold(true).Render(ui.Truncate(c.Name, width))
	if cost := deref(c.ManaCost); cost != "" {
		name += "  " + m.theme.ManaCost(cost)
	}
	out = append(out, ui.Truncate(name, width))

	if line := joinNonEmpty(" · ", c.TypeLine, c.Rarity); line != "" {
		out = append(out, ui.Truncate(line, width))
	}

	if !c.Enriched {
		out = append(out, dim("card details not stored yet · press : and run UpdatePrices"))
	}

	if c.OracleText != "" {
		out = append(out, "")
		for _, para := range strings.Split(c.OracleText, "\n") {
			out = append(out, wrapHang(para, cardW)...)
		}
	}
	if flavor := c.FlavorText; flavor != "" {
		out = append(out, "")
		for _, para := range strings.Split(flavor, "\n") {
			for _, line := range wrap(para, cardW) {
				out = append(out, dim(line))
			}
		}
	}

	if stat := statBox(c); stat != "" {
		styled := m.theme.Title.Render(stat)
		out = append(out, strings.Repeat(" ", max(cardW-ansi.StringWidth(stat), 0))+styled)
	}

	footer := joinNonEmpty(" · ",
		c.Artist, c.SetName,
		ui.Printing(c.SetCode, c.CollectorNumber), c.ReleasedAt)
	out = append(out, dim(ui.Truncate(footer, width)))
	return out
}

func (m Model) hoardLines(d detail, width int) []string {
	dim := m.theme.Help.Render
	var out []string

	out = append(out, "", m.theme.Title.Render("HELD"))
	if len(d.holdings) == 0 {
		out = append(out, dim("  nothing: this printing is catalogued but not held"))
	}

	finishCell := func(h store.Holding) string {
		s := ui.FinishTreated(h.Finish, h.Treatment)
		if h.Guessed {
			s += "?"
		}
		return s
	}
	var qtyW, setW, finW, condW int
	for _, h := range d.holdings {
		qtyW = max(qtyW, ansi.StringWidth(ui.Qty(h.Quantity)))
		setW = max(setW, ansi.StringWidth(ui.Printing(h.SetCode, h.CollectorNumber)))
		condW = max(condW, ansi.StringWidth(ui.Condition(h.Condition)))
		finW = max(finW, ansi.StringWidth(finishCell(h)))
	}
	pad := func(s string, w int, left bool) string {
		fill := strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
		if left {
			return fill + s
		}
		return s + fill
	}
	for i, h := range d.holdings {
		where := h.ContainerName
		if h.ContainerKind != store.KindCollection && h.Board != "main" {
			where += " (" + h.Board + ")"
		}
		qty := pad(ui.Qty(h.Quantity), qtyW, true)
		set := pad(ui.Printing(h.SetCode, h.CollectorNumber), setW, false)
		fin := pad(finishCell(h), finW, false)
		cond := pad(ui.Condition(h.Condition), condW, false)

		if i == d.heldCursor && d.zone == zoneHeld {
			mark := func(s string, f int) string {
				if d.heldField == f {
					return ui.Restyle(s, m.theme.Cursor)
				}
				return s
			}
			parts := []string{mark(qty, fieldQty), mark(set, fieldSet),
				mark(fin, fieldFinish), mark(cond, fieldCondition),
				mark(where, fieldWhere)}
			out = append(out, ui.Truncate("▸ "+strings.Join(parts, " · "), width))
			continue
		}
		line := ui.Truncate("  "+strings.Join([]string{qty, set, fin, cond, where}, " · "), width)

		if i == d.heldCursor && len(d.holdings) > 1 {
			line = ui.Restyle(fit(line, min(width, frameWidth)), m.theme.Inactive)
		}
		out = append(out, line)
	}

	out = append(out, "", m.theme.Title.Render("PRICE"))

	var groups [][]string
	for _, fin := range []finish.Finish{finish.Nonfoil, finish.Foil} {
		var g []string

		s, b := sharedWindow(d.series[fin], d.bids[fin])
		if len(s) > 0 {
			label := finishLabel(fin, d.card.Treatment)
			spark := ui.Spark(ui.Resample(pricePoints(s), sparkCells), sparkCells)
			now := s[len(s)-1].Price
			line := fmt.Sprintf("  %-9s %s  %s", label, spark, m.theme.Title.Render(ui.Money(now)))

			if first := s[0].Price; len(s) > 1 && now != first {
				change := ui.SignedMoney(now - first)
				if pct := ui.SignedPercent(safeFrac(now-first, first)); pct != "" {
					change += " (" + pct + ")"
				}
				line += "  " + change
			}
			caption := dim(fmt.Sprintf("  %-9s %s", "", seriesRange(s)))
			if len(s) < 2 {
				caption = dim(fmt.Sprintf("  %-9s first seen %s · run backfill for 90 days",
					"", seriesSince(s)))
			}
			if run := m.streakPhrase(store.Streak(s)); run != "" {
				caption += dim(" · ") + run
			}
			g = append(g, line, caption)
		}

		g = append(g, m.bidLines(s, b, !d.fromArbitrage)...)
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		out = append(out, dim("  no history yet · press : and run BackfillPriceHistory90"))
	}
	for i, g := range groups {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, g...)
	}

	out = append(out, m.compLines(d, width)...)
	out = append(out, m.linkLines(d, width)...)
	return out
}

func (m Model) linkLines(d detail, width int) []string {
	if m.openURL == nil || len(d.links) == 0 {
		return nil
	}
	parts := make([]string, len(d.links))
	for i, l := range d.links {
		if i == d.linkCursor {

			style := m.theme.Cursor
			if d.zone != zoneLinks {
				style = m.theme.Inactive
			}
			parts[i] = ui.Restyle(" "+l.name+" ", style)
			continue
		}
		parts[i] = m.theme.Help.Render(" " + l.name + " ")
	}
	return []string{"", m.theme.Title.Render("LINKS"),
		ui.Truncate("  "+strings.Join(parts, " "), width)}
}

func sharedWindow(s, b []store.PricePoint) ([]store.PricePoint, []store.PricePoint) {
	if len(s) == 0 || len(b) == 0 {
		return s, b
	}
	start := max(s[0].AsOf, b[0].AsOf)
	if min(s[len(s)-1].AsOf, b[len(b)-1].AsOf) <= start {

		return s, b
	}
	end := max(s[len(s)-1].AsOf, b[len(b)-1].AsOf)
	return clipToWindow(s, start, end), clipToWindow(b, start, end)
}

func (m Model) bidLines(retail, b []store.PricePoint, withSpread bool) []string {
	if len(b) == 0 {
		return nil
	}
	dim := m.theme.Help.Render
	var out []string

	spark := ui.Spark(ui.Resample(pricePoints(b), sparkCells), sparkCells)
	now := b[len(b)-1].Price
	out = append(out, "  "+dim(fmt.Sprintf("%-9s", "buylist"))+" "+spark+"  "+
		m.theme.Title.Render(ui.Money(now)))
	out = append(out, dim(fmt.Sprintf("  %-9s %s", "", seriesRange(b))))

	if vals, since, ok := spreadSeries(retail, b, sparkCells); ok && withSpread {
		last, first := vals[len(vals)-1], vals[0]
		word := "steady"
		switch {
		case last < first-0.005:
			word = "tightening"
		case last > first+0.005:
			word = "widening"
		}
		caption := fmt.Sprintf("%s → %s since %s · %s",
			ui.PercentAlways(first), ui.PercentAlways(last), since, word)
		out = append(out, "  "+dim(fmt.Sprintf("%-9s", "spread"))+" "+
			ui.Spark(vals, sparkCells)+"  "+
			m.env.Heat(market.MarkupGrade(last))(ui.PercentAlways(last))+"  "+dim(caption))
	}
	return out
}

func (m Model) compLines(d detail, width int) []string {
	if m.cardComps == nil {
		return nil
	}
	dim := m.theme.Help.Render
	out := []string{"", m.theme.Title.Render("COMPS")}
	if d.compsPending {

		return append(out, dim("  reading today's vendor quotes…"))
	}
	if !d.compsOK {
		return append(out, dim("  no vendor quotes today · press F on the MARKET view to fetch"))
	}
	if len(d.comps) == 0 {
		return append(out, dim("  no vendor quoted this printing today"))
	}

	env := ui.Env{Width: max(width-2, 0), Color: m.env.Color, Clamp: true}
	t := ui.Table{Env: env, Header: true, Cols: []ui.Col{
		{Align: ui.Left, Style: env.Dim()},
		{Title: "TCG SOLD", Align: ui.Right},
		{Title: "MP", Align: ui.Right, Priority: 2, Style: env.Dim()},
		{Title: "CK", Align: ui.Right, Priority: 1, Style: env.Dim()},
		{Title: "CK PAYS", Align: ui.Right},
		{Title: "SPREAD", Align: ui.Right},
	}}

	for _, fin := range finish.All() {
		c, ok := d.comps[fin]
		if !ok {
			continue
		}
		t.Add(ui.C(finishLabel(fin, d.card.Treatment)),
			ui.C(compMoney(c.HasMarket, c.Market)),
			ui.C(compMoney(c.HasManapool, c.Manapool)),
			ui.C(compMoney(c.HasCK, c.CK)),
			ui.C(compMoney(c.HasBuylist, c.Buylist)),
			compSpreadCell(env, c))
	}
	for _, line := range t.Lines() {
		out = append(out, "  "+line)
	}
	return out
}

func spreadSeries(retail, bids []store.PricePoint, buckets int) (vals []float64, since string, ok bool) {
	if len(retail) == 0 || len(bids) == 0 {
		return nil, "", false
	}
	start := max(retail[0].AsOf, bids[0].AsOf)
	end := min(retail[len(retail)-1].AsOf, bids[len(bids)-1].AsOf)
	if end <= start {
		return nil, "", false
	}
	r := ui.Resample(pricePoints(clipToWindow(retail, start, end)), buckets)
	b := ui.Resample(pricePoints(clipToWindow(bids, start, end)), buckets)
	if len(r) != len(b) || len(r) == 0 {
		return nil, "", false
	}
	vals = make([]float64, len(r))
	defined := false
	prev := 0.0
	for i := range r {
		if r[i] > 0 {
			prev = 1 - b[i]/r[i]
			defined = true
		}
		vals[i] = prev
	}
	if !defined {
		return nil, "", false
	}
	since = start
	if t, err := time.Parse(time.RFC3339, start); err == nil {
		since = t.Local().Format("2 Jan")
	}
	return vals, since, true
}

func clipToWindow(s []store.PricePoint, start, end string) []store.PricePoint {
	var out []store.PricePoint
	var carry *store.PricePoint
	for _, p := range s {
		switch {
		case p.AsOf < start:
			q := p
			carry = &q
		case p.AsOf <= end:
			if carry != nil {
				c := *carry
				c.AsOf = start
				out = append(out, c)
				carry = nil
			}
			out = append(out, p)
		}
	}
	if carry != nil {

		c := *carry
		c.AsOf = start
		out = append(out, c)
	}
	if n := len(out); n > 0 && out[n-1].AsOf < end {
		c := out[n-1]
		c.AsOf = end
		out = append(out, c)
	}
	return out
}

func seriesSince(s []store.PricePoint) string {
	if t, err := time.Parse(time.RFC3339, s[0].AsOf); err == nil {
		return t.Local().Format("2 Jan")
	}
	return s[0].AsOf
}

func seriesRange(s []store.PricePoint) string {
	lo, hi := s[0].Price, s[0].Price
	for _, p := range s {
		lo = min(lo, p.Price)
		hi = max(hi, p.Price)
	}
	return fmt.Sprintf("%s–%s · %d checks since %s",
		ui.Money(lo), ui.Money(hi), len(s), seriesSince(s))
}

func safeFrac(delta, base float64) float64 {
	if base == 0 {
		return 0
	}
	return delta / base
}

func statBox(c store.CardDetail) string {
	if p, t := c.Power, c.Toughness; p != "" || t != "" {
		return p + "/" + t
	}
	if l := c.Loyalty; l != "" {
		return "loyalty " + l
	}
	return ""
}

func wrapHang(s string, width int) []string {
	const marker = "• "
	if !strings.HasPrefix(s, marker) {
		return wrap(s, width)
	}
	w := ansi.StringWidth(marker)
	inner := wrap(strings.TrimPrefix(s, marker), max(width-w, 1))
	out := make([]string, len(inner))
	for i, l := range inner {
		if i == 0 {
			out[i] = marker + l
			continue
		}
		out[i] = strings.Repeat(" ", w) + l
	}
	return out
}

func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if ansi.StringWidth(line)+1+ansi.StringWidth(w) > width {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(out, line)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

const minStreak = 3

func (m Model) streakPhrase(n int) string {
	if n > -minStreak && n < minStreak {
		return ""
	}
	run, word := n, "up"
	if n < 0 {
		run, word = -n, "down"
	}
	frac := market.StreakGrade(run)
	if n < 0 {
		frac = -frac
	}
	return m.env.Diverge(frac)(fmt.Sprintf("%d %s", run, word))
}
