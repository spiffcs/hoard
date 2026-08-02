package browse

import (
	"fmt"
	"strings"
	"time"

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
	err      error
}

// openDetail loads the selected card's detail.
//
// Each view indexes its own row slice, so every view that names a single
// printing needs its own case here; movers is the odd one out, aggregating
// across finishes.
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
		// So does an arbitrage row — the quotes describe a printing you own.
		if i := m.cursor[paneCards]; i >= 0 && i < len(m.marketRows) {
			id = m.marketRows[i].Card.ScryfallID
		}
	case viewUnpriced:
		// And an unpriced row — the gap is the price, not the card.
		if i := m.cursor[paneCards]; i >= 0 && i < len(m.unpriced) {
			id = m.unpriced[i].ScryfallID
		}
	default:
		m.status, m.statusErr = "card detail works on holdings, watches, market and unpriced — press v to come back", true
		return
	}
	if id == "" {
		return
	}
	d := detail{series: map[string][]store.PricePoint{}}

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
	}
	m.detail = &d
}

// detailLines renders the overlay.
func (m Model) detailLines(d detail, width int) []string {
	dim := helpStyle.Render
	var out []string
	add := func(format string, args ...any) {
		out = append(out, ui.Truncate(fmt.Sprintf(format, args...), width))
	}

	c := d.card
	out = append(out, titleStyle.Render(ui.Truncate(c.Name, width)))

	// The type line and mana cost sit together the way they do on the card.
	if line := joinNonEmpty("  ", deref(c.TypeLine), deref(c.ManaCost)); line != "" {
		add("%s", line)
	}
	printing := ui.Printing(c.SetCode, c.CollectorNumber)
	if c.SetName != nil {
		printing = *c.SetName + " · " + printing
	}
	if c.Rarity != nil {
		printing += " · " + *c.Rarity
	}
	out = append(out, dim(ui.Truncate(printing, width)))
	if meta := joinNonEmpty(" · ", deref(c.Artist), deref(c.ReleasedAt)); meta != "" {
		out = append(out, dim(ui.Truncate(meta, width)))
	}

	// An un-refreshed card has none of the above. Say why once rather than
	// leaving the reader to wonder what happened to the card's details.
	if !c.Enriched {
		out = append(out, dim("card details not stored yet — press : and run Update prices"))
	}

	if c.OracleText != nil && *c.OracleText != "" {
		out = append(out, "")
		for _, para := range strings.Split(*c.OracleText, "\n") {
			out = append(out, wrap(para, width)...)
		}
	}

	out = append(out, "", titleStyle.Render("HELD"))
	if len(d.holdings) == 0 {
		out = append(out, dim("  nothing — this printing is catalogued but not held"))
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
		add("  %s", strings.Join(append(parts, where), " · "))
	}

	out = append(out, "", titleStyle.Render("PRICE"))
	if len(d.series) == 0 {
		out = append(out, dim("  no history yet — press : and run Backfill 90 days of price history"))
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
		line := fmt.Sprintf("  %-9s %s  %s", label, spark, titleStyle.Render(ui.Money(now)))
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

// wrap breaks a paragraph to width, on spaces.
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
		if len(line)+1+len(w) > width {
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
