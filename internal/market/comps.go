package market

// The comps section: how sellers actually value a card — triangulating the
// sales-derived market price, the asks you could buy at, and the buylist
// bid a dealer will really pay — with the spread between ask and bid as
// the confidence signal the whole hobby reads. This file reduces one
// card's vendor quotes to that comp sheet; the two frontends only render.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// Comp is one owned printing's comp sheet: every vendor's number for the
// owned finish, plus the derived low ask and the retail-to-buylist spread.
// Low includes tcgplayer's sales-derived figure, matching Assess's BuyAt.
// USD only, like everything here (cardmarket's EUR stays excluded); the
// buylist side is cardkingdom's alone today — the only buylist in the
// MTGJSON feed since TCGplayer closed its program.
type Comp struct {
	Card store.OwnedFinish

	Market      float64 // tcgplayer's sales-derived anchor
	CK          float64 // cardkingdom's retail ask
	Manapool    float64 // manapool's ask
	HasMarket   bool
	HasCK       bool
	HasManapool bool

	Low     float64 // cheapest retail across vendors
	LowFrom string

	Buylist    float64 // best buylist bid
	BuylistTo  string
	HasBuylist bool
}

// Printing is the set/number label for this comp's card.
func (c Comp) Printing() string {
	return ui.Printing(c.Card.SetCode, c.Card.CollectorNumber)
}

// HasSpread reports whether the spread is defined: it needs both sides.
func (c Comp) HasSpread() bool { return c.HasBuylist && c.Low > 0 }

// Spread is the fraction of the low ask a dealer's bid does not cover —
// the hobby's confidence signal. 20-30% marks a liquid staple, ~50% is
// typical, 80-90% means the retail price is not real yet.
func (c Comp) Spread() float64 { return 1 - c.Buylist/c.Low }

// SaleSpread is how much the sale prices on the sheet disagree: the gap
// between the highest and lowest of the present figures (TCG last-sold,
// MP ask, CK ask), as a fraction of the highest. ok is false with fewer
// than two figures — one price agrees with nothing.
func (c Comp) SaleSpread() (float64, bool) {
	var prices []float64
	if c.HasMarket {
		prices = append(prices, c.Market)
	}
	if c.HasManapool {
		prices = append(prices, c.Manapool)
	}
	if c.HasCK {
		prices = append(prices, c.CK)
	}
	if len(prices) < 2 {
		return 0, false
	}
	lo, hi := prices[0], prices[0]
	for _, p := range prices[1:] {
		lo = min(lo, p)
		hi = max(hi, p)
	}
	if hi <= 0 {
		return 0, false
	}
	return 1 - lo/hi, true
}

// saleSpreadTight and saleSpreadWide anchor the display heat: within 5%
// the vendors effectively agree, past 50% they are naming different
// cards.
const (
	saleSpreadTight = 0.05
	saleSpreadWide  = 0.50
)

// SaleSpreadGrade positions a sale spread on 0..1 for the heat ramp,
// high = disagreement: at or under 5% sits at the green end, 50% and past
// saturates the red.
func SaleSpreadGrade(s float64) float64 { return grade(s, saleSpreadTight, saleSpreadWide) }

// Verdict classifies a comp sheet the way the market view's sections do:
// a bid over the sales price is arbitrage, a bid at liquidFloor or better
// is easy to sell, anything else earns no verdict. Shares the sections'
// constants so the two surfaces cannot drift.
func (c Comp) Verdict() (Kind, bool) {
	if !c.HasBuylist || !c.HasMarket || c.Market <= 0 {
		return 0, false
	}
	if c.Buylist > c.Market {
		return KindProfit, true
	}
	if c.Buylist/c.Market >= liquidFloor {
		return KindLiquid, true
	}
	return 0, false
}

// MarkupGrade positions the retail-vs-buylist spread on 0..1 for the heat
// ramp. The scale is the spread itself: everything at or below zero sits
// on the green end — a bid at or over the ask is pure value — and the
// ramp reddens linearly to saturation at a 100% spread, the retailer
// keeping the whole sale price. Large divergence between what a card
// sells for and what the buylist pays is the noise this color flags.
func MarkupGrade(s float64) float64 { return min(max(s, 0), 1) }

// CompsTitle and CompsNote are the section heading, defined here so the
// two frontends cannot disagree about what the table claims.
const (
	CompsTitle = "COMPS"
	CompsNote  = "a list comparing vendor prices"
)

// AssessComp reduces one card's quotes to the per-vendor comp sheet for
// the finish actually owned — the same finish translation as Assess.
func AssessComp(o store.OwnedFinish, qs []mtgjson.Quote) Comp {
	c := Comp{Card: o}
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
			switch q.Provider {
			case MarketProvider:
				c.Market, c.HasMarket = q.Price, true
			case "cardkingdom":
				c.CK, c.HasCK = q.Price, true
			case "manapool":
				c.Manapool, c.HasManapool = q.Price, true
			}
			if c.LowFrom == "" || q.Price < c.Low {
				c.Low, c.LowFrom = q.Price, q.Provider
			}
		case mtgjson.Buylist:
			if q.Price > c.Buylist {
				c.Buylist, c.BuylistTo, c.HasBuylist = q.Price, q.Provider, true
			}
		}
	}
	c.dropTrollListings()
	return c
}

// listingOutlierRatio matches the pricing layer's troll-listing guard: a
// marketplace's "lowest ask" can be a joke listing (a $7,362,059.74
// Legion Loyalty, observed live), and a comp sheet quoting it beside the
// $2.49 the card actually sells for is noise wearing a money column.
const listingOutlierRatio = 20

// dropTrollListings clears any sale figure over listingOutlierRatio times
// the cheapest other one on the sheet, and re-derives Low without it. A
// lone figure is trusted — with one voice there is nothing to compare.
func (c *Comp) dropTrollListings() {
	type figure struct {
		p   *float64
		has *bool
	}
	figs := []figure{{&c.Market, &c.HasMarket}, {&c.CK, &c.HasCK}, {&c.Manapool, &c.HasManapool}}
	var present []float64
	for _, f := range figs {
		if *f.has {
			present = append(present, *f.p)
		}
	}
	if len(present) < 2 {
		return
	}
	cheapest := present[0]
	for _, p := range present {
		cheapest = min(cheapest, p)
	}
	c.Low, c.LowFrom = 0, ""
	names := []string{MarketProvider, "cardkingdom", "manapool"}
	for i, f := range figs {
		if !*f.has {
			continue
		}
		if cheapest > 0 && *f.p > cheapest*listingOutlierRatio {
			*f.p, *f.has = 0, false
			continue
		}
		if c.LowFrom == "" || *f.p < c.Low {
			c.Low, c.LowFrom = *f.p, names[i]
		}
	}
}

// TopComps returns the first limit comp sheets. Result.Comps arrives
// value-ranked from Collect; the copy lets callers filter in place.
func TopComps(comps []Comp, limit int) []Comp {
	if limit <= 0 || limit > len(comps) {
		limit = len(comps)
	}
	return append([]Comp(nil), comps[:limit]...)
}

// sortComps ranks comp sheets by what the copies are worth, with the same
// full tiebreak as top(): truncation must not shuffle between runs.
func sortComps(comps []Comp) {
	slices.SortStableFunc(comps, func(a, b Comp) int {
		if c := cmp.Compare(b.Card.Value, a.Card.Value); c != 0 {
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
}
