// Package arbitrage compares what different vendors will pay and charge for the
// cards you already own.
//
// It is analysis only: callers supply the holdings and the vendor quotes, and
// get back ranked opportunities. Fetching is deliberately left outside, because
// the two callers need it to happen very differently — a CLI command blocks on
// the download and prints, while the browser has to keep drawing frames and stay
// interruptible.
package market

import (
	"cmp"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// MarketProvider is the vendor whose figure anchors every comparison.
// TCGplayer's price in MTGJSON is its market price — computed from actual
// completed sales, not from what anyone is asking — so it is the one number
// here that describes what cards really trade at. Everything else renders
// as a deviation from it.
const MarketProvider = "tcgplayer"

// Opportunity is one owned printing-and-finish, with the vendor quotes that make
// it interesting.
type Opportunity struct {
	Card store.OwnedFinish

	Market    float64 // the sales-derived anchor (see MarketProvider)
	BuyAt     float64 // cheapest ask, any vendor — a listing is buyable
	BuyFrom   string
	SellAt    float64 // best buylist offer
	SellTo    string
	HasMarket bool
	HasRetail bool
	HasBuy    bool
}

// Liquidity is the fraction of the last-sold price a shop will pay.
func (o Opportunity) Liquidity() float64 { return o.SellAt / o.Market }

// LiquidityGrade positions a liquidity ratio on 0..1 for display
// gradients: the 70% floor (the least a row can pay and still be listed)
// is 0, paying the full sales price is 1. Defined beside the floor it
// reads from, so the two frontends coloring the column cannot disagree.
func LiquidityGrade(r float64) float64 { return grade(r, liquidFloor, 1.0) }

// BelowMarketGrade positions a discount on 0..1: the 25% listing floor is
// 0, and 60% under the sales price — deep-discount territory — saturates.
func BelowMarketGrade(b float64) float64 { return grade(b, belowMarketFloor, 0.6) }

// grade is the shared clamp-and-normalize.
func grade(v, lo, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	return min(max((v-lo)/(hi-lo), 0), 1)
}

// BelowMarket is how far under the last-sold price the cheapest ask sits.
func (o Opportunity) BelowMarket() float64 { return 1 - o.BuyAt/o.Market }

// Profit is what a buylist pays over the last-sold price: selling an owned
// copy there beats what the card actually trades at. Positive is genuine
// arbitrage; anchored on Market, not on anyone's ask.
func (o Opportunity) Profit() float64 { return o.SellAt - o.Market }

// Printing is the set/number label for this opportunity's card.
func (o Opportunity) Printing() string {
	return ui.Printing(o.Card.SetCode, o.Card.CollectorNumber)
}

// Result is a whole pass over the collection.
type Result struct {
	Opportunities []Opportunity
	// Comps is every compared printing's per-vendor sheet, value-ranked —
	// the fourth section, parallel to the Kind-filtered Opportunities.
	Comps []Comp
	// Compared is how many owned printings had two or more vendors to
	// compare, reported so a caller can say how much ground the analysis
	// actually covered.
	Compared int
}

// Collect turns holdings and their quotes into ranked opportunities. quotes is
// keyed by Scryfall id, exactly as pricing.Fetcher.Quotes returns them.
//
// minValue filters on what you would actually pay, not on what hoard thinks the
// card is worth. A 900% spread between $0.20 and $1.99 is arithmetic, not an
// opportunity, and sorting by percentage would otherwise fill every section with
// cards worth less than the postage.
func Collect(owned []store.OwnedFinish, quotes map[string][]mtgjson.Quote, minValue float64) Result {
	var res Result
	for _, o := range owned {
		qs := quotes[o.ScryfallID]
		if len(qs) == 0 {
			continue
		}
		op, retailCount := Assess(o, qs)
		if retailCount >= 2 {
			res.Compared++
			// The comps population is exactly the compared one: a sheet
			// with one vendor on it compares nothing. minValue filters on
			// the low ask, same as the opportunities.
			if c := AssessComp(o, qs); c.Low >= minValue {
				res.Comps = append(res.Comps, c)
			}
		}
		if op.HasRetail && op.BuyAt >= minValue {
			res.Opportunities = append(res.Opportunities, op)
		}
	}
	sortComps(res.Comps)
	return res
}

// Assess reduces one card's quotes to the sales-price anchor, the cheapest
// ask, and the best buylist offer, for the finish actually owned.
func Assess(o store.OwnedFinish, qs []mtgjson.Quote) (op Opportunity, retailCount int) {
	op.Card = o
	// The store speaks Scryfall's finish vocabulary (nonfoil|foil|etched);
	// MTGJSON's price files speak their own ("normal"/"foil"). This is the
	// one translation point between them.
	finish := "normal"
	if scryfall.PricedAsFoil(o.Finish) {
		finish = "foil"
	}

	for _, q := range qs {
		if q.Finish != finish || q.Price <= 0 {
			continue
		}
		switch q.Kind {
		case mtgjson.Retail:
			retailCount++
			if q.Provider == MarketProvider {
				op.Market, op.HasMarket = q.Price, true
			}
			if !op.HasRetail || q.Price < op.BuyAt {
				op.BuyAt, op.BuyFrom, op.HasRetail = q.Price, q.Provider, true
			}
		case mtgjson.Buylist:
			if q.Price > op.SellAt {
				op.SellAt, op.SellTo, op.HasBuy = q.Price, q.Provider, true
			}
		}
	}
	return op, retailCount
}

// Kind is which question an opportunity answers. The same card can answer more
// than one, and appears once per question it answers.
type Kind int

const (
	// KindProfit is real arbitrage: a buylist pays more than the card last
	// sold for.
	KindProfit Kind = iota
	// KindLiquid is where a buylist pays close to the sales price — not a
	// profit, but the card is easy to turn back into money.
	KindLiquid
	// KindBelowMarket is a real ask sitting well under what the card
	// actually sells for — the useful direction of vendor disagreement.
	// The other direction (a lone listing far above the sales price) is
	// scalper noise and is deliberately not a section.
	KindBelowMarket
)

func (k Kind) String() string {
	switch k {
	case KindProfit:
		return "arbitrage"
	case KindLiquid:
		return "liquid"
	}
	return "below-market"
}

// Title and Note describe a Kind for a section heading.
func (k Kind) Title() string {
	switch k {
	case KindProfit:
		return "ARBITRAGE"
	case KindLiquid:
		return "EASY TO SELL"
	}
	return "BELOW MARKET"
}

func (k Kind) Note() string {
	switch k {
	case KindProfit:
		return "a buylist pays more than the card last sold for"
	case KindLiquid:
		return "a buylist pays at least 70% of the last-sold price"
	}
	return "a marketplace is asking far under what the card last sold for"
}

// liquidFloor is the least a buylist may pay, as a fraction of the sales
// price, to count as liquidity. Typical buylists sit near half of it; 70%
// marks the cards genuinely easy to turn back into money.
const liquidFloor = 0.7

// belowMarketFloor is how far under the sales price an ask must sit to be
// worth listing — a quarter off is a deal, a few percent is noise.
const belowMarketFloor = 0.25

// Kinds is every question, in the order they are worth reading.
var Kinds = []Kind{KindProfit, KindLiquid, KindBelowMarket}

// Section is one Kind's best rows.
type Section struct {
	Kind Kind
	Rows []Opportunity
}

// Sections ranks the result into the three questions, at most limit rows each.
func Sections(r Result, limit int) []Section {
	out := make([]Section, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, Section{Kind: k, Rows: top(r.Opportunities, limit, k)})
	}
	return out
}

// Row is one opportunity tagged with the question it answers, for callers that
// show a single flat list rather than three tables.
type Row struct {
	Opportunity
	Kind Kind
}

// Rows flattens Sections, keeping section order, so a scrolling list shows real
// arbitrage first and still says which question each row answers.
func Rows(r Result, limit int) []Row {
	var out []Row
	for _, sec := range Sections(r, limit) {
		for _, o := range sec.Rows {
			out = append(out, Row{Opportunity: o, Kind: sec.Kind})
		}
	}
	return out
}

// top filters and ranks the opportunities that answer one question.
func top(all []Opportunity, limit int, k Kind) []Opportunity {
	keep := func(o Opportunity) bool { return o.HasBuy && o.HasMarket && o.Profit() > 0 }
	order := func(a, b Opportunity) int { return cmp.Compare(b.Profit(), a.Profit()) }
	switch k {
	case KindLiquid:
		// The floor is the section's meaning: "liquid" promises a buylist
		// close to the sales price, and without a threshold the section
		// fills with whatever unprofitable rows a small collection has — a
		// shop paying 27% of it is not liquidity, it is every card ever
		// printed (observed live, and fairly called nonsense).
		keep = func(o Opportunity) bool {
			return o.HasBuy && o.HasMarket && o.Profit() <= 0 && o.Liquidity() >= liquidFloor
		}
		order = func(a, b Opportunity) int { return cmp.Compare(b.Liquidity(), a.Liquidity()) }
	case KindBelowMarket:
		keep = func(o Opportunity) bool {
			return o.HasRetail && o.HasMarket && o.BelowMarket() >= belowMarketFloor
		}
		order = func(a, b Opportunity) int { return cmp.Compare(b.BelowMarket(), a.BelowMarket()) }
	}

	var out []Opportunity
	for _, o := range all {
		if keep(o) {
			out = append(out, o)
		}
	}
	// Stable, fully tiebroken: the ranking keys tie often (two printings a
	// vendor prices identically), and truncating an unpinned order would make
	// rows near the limit appear and vanish between runs over the same data.
	slices.SortStableFunc(out, func(a, b Opportunity) int {
		if c := order(a, b); c != 0 {
			return c
		}
		if c := strings.Compare(a.Card.Name, b.Card.Name); c != 0 {
			return c
		}
		if c := strings.Compare(a.Card.ScryfallID, b.Card.ScryfallID); c != 0 {
			return c
		}
		return strings.Compare(a.Card.Finish, b.Card.Finish)
	})
	return out[:min(len(out), limit)]
}
