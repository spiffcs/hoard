// Package arbitrage compares what different vendors will pay and charge for the
// cards you already own.
//
// It is analysis only: callers supply the holdings and the vendor quotes, and
// get back ranked opportunities. Fetching is deliberately left outside, because
// the two callers need it to happen very differently — a CLI command blocks on
// the download and prints, while the browser has to keep drawing frames and stay
// interruptible.
package arbitrage

import (
	"cmp"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// OutlierFactor is how far above the cheapest quote a price may sit before it is
// treated as unsupported rather than as an opportunity.
//
// Chosen from the real spread distribution: the widest genuine disagreement
// measured was 9.3x (Siege-Gang Lieutenant, $4.49 against $41.68), while
// Manapool lists one card at 55,600x what two other vendors charge. Twenty
// leaves room on both sides of that gap.
const OutlierFactor = 20

// Opportunity is one owned printing-and-finish, with the vendor quotes that make
// it interesting.
type Opportunity struct {
	Card store.OwnedFinish

	BuyAt     float64 // cheapest retail
	BuyFrom   string
	DearAt    float64 // dearest retail that another vendor corroborates
	DearFrom  string
	SellAt    float64 // best buylist offer
	SellTo    string
	HasRetail bool
	HasBuy    bool
}

// Spread is how much dearer the priciest vendor is than the cheapest.
func (o Opportunity) Spread() float64 { return o.DearAt/o.BuyAt - 1 }

// Liquidity is the fraction of the cheapest retail price a shop will pay.
func (o Opportunity) Liquidity() float64 { return o.SellAt / o.BuyAt }

// Profit is what is left after buying at the cheapest retail and selling to the
// best buylist. Positive is genuine arbitrage.
func (o Opportunity) Profit() float64 { return o.SellAt - o.BuyAt }

// Printing is the set/number label for this opportunity's card.
func (o Opportunity) Printing() string {
	return ui.Printing(o.Card.SetCode, o.Card.CollectorNumber)
}

// Result is a whole pass over the collection.
type Result struct {
	Opportunities []Opportunity
	// Compared is how many owned printings had two or more vendors to compare;
	// Ignored is how many listings were discarded as unsupported. Both are
	// reported so a caller can say what was left out rather than hiding it.
	Compared int
	Ignored  int
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
		op, retailCount, dropped := Assess(o, qs)
		res.Ignored += dropped
		if retailCount >= 2 {
			res.Compared++
		}
		if op.HasRetail && op.BuyAt >= minValue {
			res.Opportunities = append(res.Opportunities, op)
		}
	}
	return res
}

// Assess reduces one card's quotes to the best buy, the best corroborated sell,
// and the best buylist offer, for the finish actually owned.
//
// It reports how many retail quotes were usable and how many were discarded as
// unsupported, so the caller can say so rather than silently hiding them.
func Assess(o store.OwnedFinish, qs []mtgjson.Quote) (op Opportunity, retailCount, dropped int) {
	op.Card = o
	// The store speaks Scryfall's finish vocabulary (nonfoil|foil|etched);
	// MTGJSON's price files speak their own ("normal"/"foil"). This is the
	// one translation point between them.
	finish := "normal"
	if scryfall.PricedAsFoil(o.Finish) {
		finish = "foil"
	}

	var retail []mtgjson.Quote
	for _, q := range qs {
		if q.Finish != finish || q.Price <= 0 {
			continue
		}
		switch q.Kind {
		case mtgjson.Retail:
			retail = append(retail, q)
		case mtgjson.Buylist:
			if q.Price > op.SellAt {
				op.SellAt, op.SellTo, op.HasBuy = q.Price, q.Provider, true
			}
		}
	}
	if len(retail) == 0 {
		return op, 0, 0
	}

	slices.SortFunc(retail, func(a, b mtgjson.Quote) int { return cmp.Compare(a.Price, b.Price) })
	op.BuyAt, op.BuyFrom, op.HasRetail = retail[0].Price, retail[0].Provider, true

	// Walk down from the dearest until one is close enough to the cheapest to be
	// believable. A lone vendor quoting many times what everyone else charges is
	// a broken listing, not an opportunity, and it would otherwise top the list.
	for i := len(retail) - 1; i >= 0; i-- {
		if retail[i].Price <= op.BuyAt*OutlierFactor {
			op.DearAt, op.DearFrom = retail[i].Price, retail[i].Provider
			break
		}
		dropped++
	}
	return op, len(retail) - dropped, dropped
}

// Kind is which question an opportunity answers. The same card can answer more
// than one, and appears once per question it answers.
type Kind int

const (
	// KindProfit is real arbitrage: a shop pays more than the cheapest retail.
	KindProfit Kind = iota
	// KindLiquid is where a buylist is close to retail — not a profit, but the
	// card is easy to turn back into money.
	KindLiquid
	// KindSpread is where vendors simply disagree about the price.
	KindSpread
)

func (k Kind) String() string {
	switch k {
	case KindProfit:
		return "arbitrage"
	case KindLiquid:
		return "liquid"
	}
	return "spread"
}

// Title and Note describe a Kind for a section heading.
func (k Kind) Title() string {
	switch k {
	case KindProfit:
		return "ARBITRAGE"
	case KindLiquid:
		return "EASY TO SELL"
	}
	return "CHEAPEST VS DEAREST"
}

func (k Kind) Note() string {
	switch k {
	case KindProfit:
		return "a shop pays more than the cheapest retail"
	case KindLiquid:
		return "a buylist pays at least 70% of retail"
	}
	return "where the vendors disagree"
}

// liquidFloor is the least a buylist may pay, as a fraction of the cheapest
// retail, to count as liquidity. Typical buylists sit near half of retail;
// 70% marks the cards genuinely easy to turn back into money.
const liquidFloor = 0.7

// Kinds is every question, in the order they are worth reading.
var Kinds = []Kind{KindProfit, KindLiquid, KindSpread}

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
	keep := func(o Opportunity) bool { return o.HasBuy && o.Profit() > 0 }
	order := func(a, b Opportunity) int { return cmp.Compare(b.Profit(), a.Profit()) }
	switch k {
	case KindLiquid:
		// The floor is the section's meaning: "liquid" promises a buylist
		// close to retail, and without a threshold the section fills with
		// whatever unprofitable rows a small collection has — a shop paying
		// 27% of retail is not liquidity, it is every card ever printed
		// (observed live, and fairly called nonsense).
		keep = func(o Opportunity) bool {
			return o.HasBuy && o.Profit() <= 0 && o.Liquidity() >= liquidFloor
		}
		order = func(a, b Opportunity) int { return cmp.Compare(b.Liquidity(), a.Liquidity()) }
	case KindSpread:
		keep = func(o Opportunity) bool { return o.DearAt > o.BuyAt }
		order = func(a, b Opportunity) int { return cmp.Compare(b.Spread(), a.Spread()) }
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
