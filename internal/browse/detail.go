package browse

import (
	"github.com/charmbracelet/x/ansi"

	"fmt"
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
	err     error
	// image is the rendered card image block (halfblock cells or kitty
	// placeholders), nil until the async fetch lands — or forever, when
	// the terminal can't draw one. The overlay never waits for it.
	image []string
}

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
	d := detail{
		series: map[string][]store.PricePoint{},
		bids:   map[string][]store.PricePoint{},
	}

	var err error
	if d.card, err = m.store.CardDetail(id); err != nil {
		m.setError(err)
		return
	}
	if d.holdings, err = m.store.HoldingsOf(id); err != nil {
		m.setError(err)
		return
	}
	// Both finishes, not just the one held: a card owned in non-foil is often
	// being looked at precisely because its foil is doing something.
	for _, finish := range []string{"nonfoil", "foil"} {
		s, err := m.store.PriceSeries(id, finish)
		if err != nil {
			m.setError(err)
			return
		}
		if len(s) > 0 {
			d.series[finish] = s
		}
		b, err := m.store.BidSeries(id, finish)
		if err != nil {
			m.setError(err)
			return
		}
		if len(b) > 0 {
			d.bids[finish] = b
		}
	}
	if m.cardComps != nil {
		d.comps, d.compsOK = m.cardComps(id)
	}
	m.detail = &d
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
		out = append(out, dim("card details not stored yet · press : and run Update prices"))
	}

	if c.OracleText != nil && *c.OracleText != "" {
		out = append(out, "")
		for _, para := range strings.Split(*c.OracleText, "\n") {
			out = append(out, wrap(para, cardW)...)
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
	for _, h := range d.holdings {
		where := h.ContainerName
		if h.ContainerKind != store.KindCollection && h.Board != "main" {
			where += " (" + h.Board + ")"
		}
		// The finish is named only when it isn't normal — the table columns'
		// rule, minus the placeholder dash, which in a list reads as a stray mark.
		parts := []string{ui.Qty(h.Quantity)}
		if h.Finish != "nonfoil" {
			parts = append(parts, h.Finish)
		}
		out = append(out, ui.Truncate("  "+strings.Join(append(parts, where), " · "), width))
	}

	out = append(out, "", m.theme.Title.Render("PRICE"))
	if len(d.series) == 0 {
		out = append(out, dim("  no history yet · press : and run Backfill 90 days of price history"))
	}
	for _, finish := range []string{"nonfoil", "foil"} {
		s := d.series[finish]
		if len(s) == 0 {
			continue
		}
		label := finish
		if finish == "nonfoil" {
			label = "non-foil"
		}
		spark := ui.Spark(ui.Resample(pricePoints(s), sparkCells), sparkCells)
		now := s[len(s)-1].Price
		line := fmt.Sprintf("  %-9s %s  %s", label, spark, m.theme.Title.Render(ui.Money(now)))
		// The change over the window is the question the sparkline raises;
		// answer it in the movers view's own +$/-% language. A single check has
		// no movement to report.
		if first := s[0].Price; len(s) > 1 && now != first {
			change := ui.SignedMoney(now - first)
			if pct := ui.SignedPercent(safeFrac(now-first, first)); pct != "" {
				change += " (" + pct + ")"
			}
			line += "  " + change
		}
		out = append(out, line)
		out = append(out, dim(fmt.Sprintf("  %-9s %s", "", seriesRange(s))))
		out = append(out, m.bidLines(d, finish)...)
	}
	// A bid series can exist for a finish the retail history missed —
	// the two tables have independent eras.
	for _, finish := range []string{"nonfoil", "foil"} {
		if len(d.series[finish]) == 0 && len(d.bids[finish]) > 0 {
			out = append(out, m.bidLines(d, finish)...)
		}
	}

	out = append(out, m.compLines(d, width)...)
	return out
}

// bidLines renders one finish's buylist rows under its price row: the bid
// sparkline, and — when both series overlap — the spread over time, the
// confidence signal as a trend rather than a snapshot.
func (m Model) bidLines(d detail, finish string) []string {
	b := d.bids[finish]
	if len(b) == 0 {
		return nil
	}
	dim := m.theme.Help.Render
	var out []string

	// The label pads before it dims: %-9s over a styled string counts the
	// escape bytes as width.
	spark := ui.Spark(ui.Resample(pricePoints(b), sparkCells), sparkCells)
	now := b[len(b)-1].Price
	out = append(out, "  "+dim(fmt.Sprintf("%-9s", "bid"))+" "+spark+"  "+
		m.theme.Title.Render(ui.Money(now)))
	out = append(out, dim(fmt.Sprintf("  %-9s %s", "", seriesRange(b))))

	if vals, since, ok := spreadSeries(d.series[finish], b, sparkCells); ok {
		last, first := vals[len(vals)-1], vals[0]
		word := "steady"
		switch {
		case last < first-0.005:
			word = "tightening"
		case last > first+0.005:
			word = "widening"
		}
		caption := fmt.Sprintf("%s → %s since %s · %s",
			ui.Percent(first), ui.Percent(last), since, word)
		out = append(out, "  "+dim(fmt.Sprintf("%-9s", "spread"))+" "+
			ui.Spark(vals, sparkCells)+"  "+
			m.env.Grade(market.SpreadGrade(last))(ui.Percent(last))+"  "+dim(caption))
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

	type verdict struct {
		finish string
		kind   market.Kind
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
		var parts []string
		if c.HasMarket {
			parts = append(parts, "tcg last sold "+ui.Money(c.Market))
		}
		if c.HasManapool {
			parts = append(parts, "mp asks "+ui.Money(c.Manapool))
		}
		if c.HasCK {
			parts = append(parts, "ck asks "+ui.Money(c.CK))
		}
		if c.HasBuylist {
			parts = append(parts, "ck pays "+ui.Money(c.Buylist))
		} else {
			parts = append(parts, "no bid today")
		}
		line := "  " + dim(fmt.Sprintf("%-9s", label)) + " " + strings.Join(parts, " · ")
		if c.HasSpread() {
			line += " · " + m.env.Grade(market.SpreadGrade(c.Spread()))("spread "+ui.Percent(c.Spread()))
		}
		out = append(out, ui.Truncate(line, width))
		if held[finish] {
			if k, ok := c.Verdict(); ok {
				verdicts = append(verdicts, verdict{finish, k, c})
			}
		}
	}
	for _, v := range verdicts {
		line := "  " + m.theme.Title.Render(v.kind.Title())
		if len(verdicts) > 1 {
			line += dim(" (" + v.finish + ")")
		}
		switch v.kind {
		case market.KindProfit:
			line += fmt.Sprintf(" · ck pays %s, +%s over tcg last-sold",
				ui.Money(v.c.Buylist), ui.Money(v.c.Buylist-v.c.Market))
		case market.KindLiquid:
			line += fmt.Sprintf(" · ck pays %s of tcg last-sold",
				ui.Percent(v.c.Buylist/v.c.Market))
		}
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
