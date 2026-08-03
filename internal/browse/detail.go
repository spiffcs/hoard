package browse

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// sparkCells is how wide a price history is drawn. Wide enough for ninety days
// of movement to have a shape, narrow enough to sit beside a label.
const sparkCells = 32

// detail is everything the detail overlay shows about one printing, gathered in
// one go so the view never reads the database while rendering.
type detail struct {
	card     store.CardDetail
	holdings []store.Holding
	series   map[string][]store.PricePoint // by finish
	bids     map[string][]store.PricePoint // by finish, from the bid history
	// comps is today's per-vendor sheet by price finish; compsOK false
	// means no day cache existed to read (the section says how to fetch).
	comps   map[string]market.Comp
	compsOK bool
	// links are the vendor pages for this printing; linkCursor is which
	// one enter opens. Both inert when no opener was injected.
	links      []cardLink
	linkCursor int
	// heldCursor selects a row of the held list; landing on a different
	// printing re-points the whole overlay (series, comps, art, links).
	heldCursor int
	// zone is which strip of the overlay the arrow keys drive: the links
	// row by default, or the held list after ↑ climbs into it. In the held
	// zone ←/→ pick a field of the selected row and enter edits it.
	zone int
	// heldField is the held row's highlighted field: quantity, printing,
	// or location.
	heldField int
	// imagePending marks an art fetch in flight: the layout reserves the
	// image's space so HELD and everything under it stay put when the art
	// lands, instead of jumping down by an image's height (observed live).
	imagePending bool
	err          error
	// image is the rendered card image block (halfblock cells or kitty
	// placeholders), nil until the async fetch lands — or forever, when
	// the terminal can't draw one. The overlay never waits for it.
	image []string
}

// The overlay's arrow-key zones, and the held row's editable fields.
const (
	zoneLinks = iota
	zoneHeld
)

const (
	fieldQty = iota
	fieldSet
	fieldWhere
	heldFieldCount
)

// openDetail loads the selected card's detail.
//
// Each view indexes its own row slice, so every view needs its own case
// here — but every view's rows name a single printing, so enter answers
// "what is this card?" everywhere.
func (m *Model) openDetail() {
	var id string
	switch m.view {
	case viewHoldings:
		if c := m.selectedCard(); c != nil {
			id = c.ScryfallID
		}
	case viewWatches:
		// A watch row names one printing just as well as a holding row.
		if w := m.selectedWatch(); w != nil {
			id = w.ScryfallID
		}
	case viewMarket:
		// So does an arbitrage row — the quotes describe a printing you
		// own — and a comps row past the Kind sections just the same.
		if c := m.selectedComp(); c != nil {
			id = c.Card.ScryfallID
		} else if i := m.cursor[paneCards]; i >= 0 && i < len(m.marketRows) {
			id = m.marketRows[i].Card.ScryfallID
		}
	case viewUnpriced:
		// And an unpriced row — the gap is the price, not the card.
		if i := m.cursor[paneCards]; i >= 0 && i < len(m.unpriced) {
			id = m.unpriced[i].ScryfallID
		}
	case viewMovers:
		// And a mover — each row is one printing in one finish, and the
		// detail's sparklines answer the question its delta raises.
		if i := m.cursor[paneCards]; i >= 0 && i < len(m.movers) {
			id = m.movers[i].ScryfallID
		}
	}
	if id == "" {
		return
	}
	var d detail
	if !m.loadPrinting(&d, id) {
		return
	}
	// Holdings by name, not id: the held list is the printing selector —
	// ten Forests can be four printings with four prices and four arts,
	// and scrolling the list re-points the whole overlay (openDetail's
	// per-view cases above only pick where the cursor starts).
	var err error
	if d.holdings, err = m.store.HoldingsOfName(d.card.Name); err != nil {
		m.setError(err)
		return
	}
	if len(d.holdings) == 0 {
		// A catalogued-but-unheld card (a watch on something you sold)
		// still shows its container-level answer: nothing.
		if d.holdings, err = m.store.HoldingsOf(id); err != nil {
			m.setError(err)
			return
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
}

// reloadDetail re-reads the open overlay's data in place after something
// changed the world under it — a price refresh finishing, an edit. The
// held cursor and the art stay; the numbers follow.
func (m *Model) reloadDetail() {
	d := m.detail
	if d == nil {
		return
	}
	if !m.loadPrinting(d, d.card.ScryfallID) {
		return
	}
	holdings, err := m.store.HoldingsOfName(d.card.Name)
	if err != nil {
		m.setError(err)
		return
	}
	if len(holdings) > 0 {
		d.holdings = holdings
	}
	d.heldCursor = min(max(d.heldCursor, 0), max(len(d.holdings)-1, 0))
	m.refreshLinks(d)
}

// refreshLinks rebuilds the vendor links for the printing and finish the
// held cursor is on — the Card Kingdom page differs per finish, so links
// follow the cursor even when the printing does not change.
func (m *Model) refreshLinks(d *detail) {
	if m.openURL == nil {
		return
	}
	cur := d.linkCursor
	d.links = cardLinks(d.card, heldFoil(d))
	d.linkCursor = min(max(cur, 0), max(len(d.links)-1, 0))
}

// heldFoil is the foilness of the holding under the held cursor — what
// decides which of a vendor's per-finish pages to link.
func heldFoil(d *detail) bool {
	if len(d.holdings) == 0 {
		return false
	}
	h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
	return scryfall.PricedAsFoil(h.Finish)
}

// loadPrinting fills the printing-scoped half of a detail — the card, its
// price and bid series, comps, links — leaving the held list alone. The
// image is also left alone: on a printing switch the old art holds the
// layout steady until the new art replaces it in place (blanking it made
// every section jump left and back, observed live); the caller decides
// when no replacement is coming and the stale art must go.
func (m *Model) loadPrinting(d *detail, id string) bool {
	d.series = map[string][]store.PricePoint{}
	d.bids = map[string][]store.PricePoint{}

	var err error
	if d.card, err = m.store.CardDetail(id); err != nil {
		m.setError(err)
		return false
	}
	// Both finishes, not just the one held: a card owned in non-foil is often
	// being looked at precisely because its foil is doing something.
	for _, finish := range []string{"nonfoil", "foil"} {
		s, err := m.store.PriceSeries(id, finish)
		if err != nil {
			m.setError(err)
			return false
		}
		if len(s) > 0 {
			d.series[finish] = s
		}
		b, err := m.store.BidSeries(id, finish)
		if err != nil {
			m.setError(err)
			return false
		}
		if len(b) > 0 {
			d.bids[finish] = b
		}
	}
	if m.cardComps != nil {
		d.comps, d.compsOK = m.cardComps(id)
	}
	return true
}

// moveHeldCursor walks the held list; landing on a different printing
// re-points the overlay at it — series, comps, links, and (via the
// returned command) the art. Landing on the same printing in another
// finish still refreshes the links: Card Kingdom's page is per finish.
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
	var cmd tea.Cmd
	if sel := d.holdings[next]; sel.ScryfallID != "" && sel.ScryfallID != d.card.ScryfallID {
		if !m.loadPrinting(d, sel.ScryfallID) {
			return nil
		}
		cmd = m.fetchDetailImage()
		if cmd == nil {
			// No replacement is coming (no URL, no capable terminal):
			// keeping the old printing's art would caption the wrong card.
			d.image = nil
		}
	}
	m.refreshLinks(d)
	return cmd
}

// cardLink is one vendor page the detail can open.
type cardLink struct {
	name string
	url  string
}

// cardLinks builds the vendor pages for a printing, in the finish the
// held cursor is on. TCGplayer addresses the exact product when the
// enrichment stored its id (v14); manapool always does (set, number, name
// slug); Card Kingdom does once the resolver has stored its links (v15) —
// foil holdings get the foil page — and falls back to a name search until
// then.
func cardLinks(c store.CardDetail, foil bool) []cardLink {
	q := url.QueryEscape(c.Name)
	tcg := "https://www.tcgplayer.com/search/magic/product?q=" + q
	if c.TCGplayerID != nil {
		tcg = fmt.Sprintf("https://www.tcgplayer.com/product/%d", *c.TCGplayerID)
	}
	// Foil holdings prefer the foil page, then the card's plain product
	// page — still the exact card — and only then the name search.
	ck := "https://www.cardkingdom.com/catalog/search?search=header&filter%5Bname%5D=" + q
	switch {
	case foil && deref(c.CKFoilURL) != "":
		ck = *c.CKFoilURL
	case deref(c.CKURL) != "":
		ck = *c.CKURL
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

// nameSlug lowercases a card name into a URL path segment: runs of
// anything but letters and digits become single hyphens.
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

// frameWidth is the widest the card-frame block draws. Oracle text at a
// couple hundred columns is a ribbon, not a text box, and the stat line is
// anchored to this block's right edge — the terminal's far corner would
// disown it.
const frameWidth = 66

// detailLines renders the overlay: the card first, laid out the way the
// physical card is — name and cost, type and rarity, the text box, flavor,
// the stat box bottom-right, the artist line as the frame's footer — then
// hoard's own facts (holdings, prices) below it. Card first also means the
// no-scroll overflow eats hoard data, never the card. The two halves are
// separate functions so a narrow terminal can stack the card image between
// them (see detailView).
func (m Model) detailLines(d detail, width int) []string {
	return append(m.cardFrameLines(d, width), m.hoardLines(d, width)...)
}

// cardFrameLines is the card half of the overlay.
func (m Model) cardFrameLines(d detail, width int) []string {
	dim := m.theme.Help.Render
	var out []string

	c := d.card
	cardW := min(width, frameWidth)

	// The name wears its identity — bold, tinted the way its row in the
	// table is — with the printed cost beside it, symbols in pip colors.
	name := m.theme.Identity(c.ColorIdentity).Bold(true).Render(ui.Truncate(c.Name, width))
	if cost := deref(c.ManaCost); cost != "" {
		name += "  " + m.theme.ManaCost(cost)
	}
	out = append(out, ui.Truncate(name, width))

	if line := joinNonEmpty(" · ", deref(c.TypeLine), deref(c.Rarity)); line != "" {
		out = append(out, ui.Truncate(line, width))
	}

	// An un-refreshed card has none of the frame fields. Say why once
	// rather than leaving the reader to wonder what happened to the card.
	if !c.Enriched {
		out = append(out, dim("card details not stored yet · press : and run UpdatePrices"))
	}

	if c.OracleText != nil && *c.OracleText != "" {
		out = append(out, "")
		for _, para := range strings.Split(*c.OracleText, "\n") {
			out = append(out, wrapHang(para, cardW)...)
		}
	}
	if flavor := deref(c.FlavorText); flavor != "" {
		out = append(out, "")
		for _, para := range strings.Split(flavor, "\n") {
			for _, line := range wrap(para, cardW) {
				out = append(out, dim(line))
			}
		}
	}
	// The stat box, bottom-right of the frame the way the card prints it:
	// power/toughness for creatures, loyalty for planeswalkers.
	if stat := statBox(c); stat != "" {
		styled := m.theme.Title.Render(stat)
		out = append(out, strings.Repeat(" ", max(cardW-len(stat), 0))+styled)
	}
	// The frame's footer: who drew it, where and when it was printed.
	footer := joinNonEmpty(" · ",
		deref(c.Artist), deref(c.SetName),
		ui.Printing(c.SetCode, c.CollectorNumber), deref(c.ReleasedAt))
	out = append(out, dim(ui.Truncate(footer, width)))
	return out
}

// hoardLines is the overlay's second half: what hoard itself knows —
// where the card is held and what its price has done.
func (m Model) hoardLines(d detail, width int) []string {
	dim := m.theme.Help.Render
	var out []string

	out = append(out, "", m.theme.Title.Render("HELD"))
	if len(d.holdings) == 0 {
		out = append(out, dim("  nothing: this printing is catalogued but not held"))
	}
	for i, h := range d.holdings {
		where := h.ContainerName
		if h.ContainerKind != store.KindCollection && h.Board != "main" {
			where += " (" + h.Board + ")"
		}
		// In the held zone the selected row shows its editable fields, the
		// highlighted one wearing the cursor: ←/→ choose, enter edits.
		if i == d.heldCursor && d.zone == zoneHeld {
			mark := func(s string, f int) string {
				if d.heldField == f {
					return ui.Restyle(s, m.theme.Cursor)
				}
				return s
			}
			parts := []string{
				mark(ui.Qty(h.Quantity), fieldQty),
				mark(ui.Printing(h.SetCode, h.CollectorNumber), fieldSet),
			}
			if h.Finish != "nonfoil" {
				parts = append(parts, h.Finish)
			}
			parts = append(parts, mark(where, fieldWhere))
			out = append(out, ui.Truncate("▸ "+strings.Join(parts, " · "), width))
			continue
		}
		// The finish is named only when it isn't normal — the table columns'
		// rule, minus the placeholder dash, which in a list reads as a stray mark.
		parts := []string{ui.Qty(h.Quantity)}
		if h.SetCode != "" || h.CollectorNumber != "" {
			parts = append(parts, ui.Printing(h.SetCode, h.CollectorNumber))
		}
		if h.Finish != "nonfoil" {
			parts = append(parts, h.Finish)
		}
		line := ui.Truncate("  "+strings.Join(append(parts, where), " · "), width)
		// The cursor bar marks the row the overlay is pointed at: ↑/↓
		// moves it, and a different printing swaps the series, comps and
		// art in place.
		if i == d.heldCursor && len(d.holdings) > 1 {
			line = ui.Restyle(fit(line, min(width, frameWidth)), m.theme.Cursor)
		}
		out = append(out, line)
	}

	out = append(out, "", m.theme.Title.Render("PRICE"))
	// One group per finish — the price rows, then that finish's bid and
	// spread rows — with a blank line between groups, so non-foil's spread
	// does not read as foil's opening act (observed live).
	var groups [][]string
	for _, finish := range []string{"nonfoil", "foil"} {
		var g []string
		// The two tables backfilled at different times, so their raw
		// series start on different dates — sparks with different "since"
		// captions read as deliberately skewed (observed live). Trim both
		// to the shared window: the overlay compares like with like, and
		// the full depth still serves the views that read one series.
		s, b := sharedWindow(d.series[finish], d.bids[finish])
		if len(s) > 0 {
			label := finish
			if finish == "nonfoil" {
				label = "non-foil"
			}
			spark := ui.Spark(ui.Resample(pricePoints(s), sparkCells), sparkCells)
			now := s[len(s)-1].Price
			line := fmt.Sprintf("  %-9s %s  %s", label, spark, m.theme.Title.Render(ui.Money(now)))
			// The change over the window is the question the sparkline
			// raises; answer it in the movers view's own +$/-% language. A
			// single check has no movement to report.
			if first := s[0].Price; len(s) > 1 && now != first {
				change := ui.SignedMoney(now - first)
				if pct := ui.SignedPercent(safeFrac(now-first, first)); pct != "" {
					change += " (" + pct + ")"
				}
				line += "  " + change
			}
			g = append(g, line, dim(fmt.Sprintf("  %-9s %s", "", seriesRange(s))))
		}
		// A bid series can exist for a finish the retail history missed —
		// the two tables have independent eras.
		g = append(g, m.bidLines(s, b)...)
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

// linkLines renders the vendor links on one line, the selected one under
// the cursor bar — ↑/↓ moves it, enter opens the page. Absent without an
// injected opener.
func (m Model) linkLines(d detail, width int) []string {
	if m.openURL == nil || len(d.links) == 0 {
		return nil
	}
	parts := make([]string, len(d.links))
	for i, l := range d.links {
		if i == d.linkCursor {
			parts[i] = ui.Restyle(" "+l.name+" ", m.theme.Cursor)
			continue
		}
		parts[i] = m.theme.Help.Render(" " + l.name + " ")
	}
	return []string{"", m.theme.Title.Render("LINKS"),
		ui.Truncate("  "+strings.Join(parts, " "), width)}
}

// sharedWindow clips a finish's retail and bid series to their common
// span for display, when both exist: aligned left edges (the later first
// observation) and right edges, via the same edge-pinning clip the spread
// math uses. Either series alone comes back untouched.
func sharedWindow(s, b []store.PricePoint) ([]store.PricePoint, []store.PricePoint) {
	if len(s) == 0 || len(b) == 0 {
		return s, b
	}
	start := max(s[0].AsOf, b[0].AsOf)
	if min(s[len(s)-1].AsOf, b[len(b)-1].AsOf) <= start {
		// Disjoint eras: stretching the stale series flat across the live
		// one would claim months-old numbers still stand. Each keeps its
		// own honest window.
		return s, b
	}
	end := max(s[len(s)-1].AsOf, b[len(b)-1].AsOf)
	return clipToWindow(s, start, end), clipToWindow(b, start, end)
}

// bidLines renders one finish's buylist rows under its price row: the bid
// sparkline, and — when both series overlap — the spread over time, the
// confidence signal as a trend rather than a snapshot. retail and b
// arrive already clipped to their shared window.
func (m Model) bidLines(retail, b []store.PricePoint) []string {
	if len(b) == 0 {
		return nil
	}
	dim := m.theme.Help.Render
	var out []string

	// The label pads before it dims: %-9s over a styled string counts the
	// escape bytes as width.
	spark := ui.Spark(ui.Resample(pricePoints(b), sparkCells), sparkCells)
	now := b[len(b)-1].Price
	out = append(out, "  "+dim(fmt.Sprintf("%-9s", "buylist"))+" "+spark+"  "+
		m.theme.Title.Render(ui.Money(now)))
	out = append(out, dim(fmt.Sprintf("  %-9s %s", "", seriesRange(b))))

	if vals, since, ok := spreadSeries(retail, b, sparkCells); ok {
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

// compLines renders the COMPS section: today's per-vendor sheet for this
// printing, in the market view's own vocabulary, with a verdict line when
// a held finish qualifies for one of its sections. Absent entirely when
// the capability was not injected.
func (m Model) compLines(d detail, width int) []string {
	if m.cardComps == nil {
		return nil
	}
	dim := m.theme.Help.Render
	out := []string{"", m.theme.Title.Render("COMPS")}
	if !d.compsOK {
		return append(out, dim("  no vendor quotes today · press F on the MARKET view to fetch"))
	}
	if len(d.comps) == 0 {
		return append(out, dim("  no vendor quoted this printing today"))
	}

	held := map[string]bool{}
	for _, h := range d.holdings {
		if scryfall.PricedAsFoil(h.Finish) {
			held["foil"] = true
		} else {
			held["nonfoil"] = true
		}
	}

	// The sheet lays out as a table — prose rows with different money
	// widths skewed every separator out of line (observed live). Same
	// column story as the market view's sell side.
	env := ui.Env{Width: max(width-2, 0), Color: m.env.Color, Clamp: true}
	t := ui.Table{Env: env, Header: true, Cols: []ui.Col{
		{Align: ui.Left, Style: env.Dim()},
		{Title: "TCG SOLD", Align: ui.Right},
		{Title: "MP", Align: ui.Right, Priority: 2, Style: env.Dim()},
		{Title: "CK", Align: ui.Right, Priority: 1, Style: env.Dim()},
		{Title: "CK PAYS", Align: ui.Right},
		{Title: "SPREAD", Align: ui.Right},
	}}
	type verdict struct {
		finish string
		c      market.Comp
	}
	var verdicts []verdict
	for _, finish := range []string{"nonfoil", "foil"} {
		c, ok := d.comps[finish]
		if !ok {
			continue
		}
		label := finish
		if finish == "nonfoil" {
			label = "non-foil"
		}
		t.Add(ui.C(label),
			ui.C(compMoney(c.HasMarket, c.Market)),
			ui.C(compMoney(c.HasManapool, c.Manapool)),
			ui.C(compMoney(c.HasCK, c.CK)),
			ui.C(compMoney(c.HasBuylist, c.Buylist)),
			compSpreadCell(env, c))
		// Only the arbitrage verdict earns a line: a bid over the sales
		// price is news; a decent bid is just the table's numbers again
		// (a liquid verdict line said nothing the CK PAYS column didn't).
		if held[finish] {
			if k, ok := c.Verdict(); ok && k == market.KindProfit {
				verdicts = append(verdicts, verdict{finish, c})
			}
		}
	}
	for _, line := range t.Lines() {
		out = append(out, "  "+line)
	}
	for _, v := range verdicts {
		line := "  " + m.theme.Title.Render(market.KindProfit.Title())
		if len(verdicts) > 1 {
			line += dim(" (" + v.finish + ")")
		}
		line += fmt.Sprintf(" · ck pays %s, +%s over tcg last-sold",
			ui.Money(v.c.Buylist), ui.Money(v.c.Buylist-v.c.Market))
		out = append(out, ui.Truncate(line, width))
	}
	return out
}

// spreadSeries derives the spread-over-time values from the retail and
// bid step functions: both clipped to their shared window so the two
// resamples share a time base — ui.Resample bases each series on its own
// first and last point, and unaligned bases would divide Tuesday's price
// by January's bid. ok is false without a real overlap.
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

// clipToWindow restricts a series to [start, end], pinning both edges:
// a synthetic point at start carries the last value at-or-before it, and
// one at end carries the last value inside the window — the step
// function's value at each edge. Resample bases a series on its own first
// and last point, so without both pins the two series' time bases would
// drift apart at whichever edge their observations stop short of.
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
		// Everything predates the window start: the step function still
		// holds its last value across it.
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

// seriesRange describes what the sparkline above it is scaled to.
//
// The glyphs are scaled to the series' own range, so without the numbers a
// dramatic-looking line could be a fifty-cent wobble. Naming the low, the high
// and how far back it goes is what makes the shape mean something.
func seriesRange(s []store.PricePoint) string {
	lo, hi := s[0].Price, s[0].Price
	for _, p := range s {
		lo = min(lo, p.Price)
		hi = max(hi, p.Price)
	}
	since := s[0].AsOf
	if t, err := time.Parse(time.RFC3339, s[0].AsOf); err == nil {
		since = t.Local().Format("2 Jan")
	}
	return fmt.Sprintf("%s–%s · %d checks since %s",
		ui.Money(lo), ui.Money(hi), len(s), since)
}

// safeFrac is delta/base, 0 when base is 0, so a card that appeared from
// nowhere shows no percentage rather than an infinite one.
func safeFrac(delta, base float64) float64 {
	if base == 0 {
		return 0
	}
	return delta / base
}

// statBox is the card's bottom-right stat: "10/10" for a creature,
// "loyalty 4" for a planeswalker, empty for everything else.
func statBox(c store.CardDetail) string {
	if p, t := deref(c.Power), deref(c.Toughness); p != "" || t != "" {
		return p + "/" + t
	}
	if l := deref(c.Loyalty); l != "" {
		return "loyalty " + l
	}
	return ""
}

// wrapHang wraps a paragraph with continuation lines hanging under the
// text rather than the margin: Scryfall writes modal abilities as "• ..."
// lines, and a continuation at column zero read as another mode
// (observed live). Paragraphs without the bullet wrap flat.
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

// wrap breaks a paragraph to width, on spaces, measuring display cells —
// em dashes and pips are one cell however many bytes they take.
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

// deref is a nullable string as text, empty when unknown.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// joinNonEmpty joins the parts that have something in them.
func joinNonEmpty(sep string, parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
