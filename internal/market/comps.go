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

// Figures counts the sale prices on the sheet — the vendors that survived
// both the finish match and the product check. Two is the floor for a comp:
// one voice compares nothing.
func (c Comp) Figures() int {
	n := 0
	for _, has := range []bool{c.HasMarket, c.HasManapool, c.HasCK} {
		if has {
			n++
		}
	}
	return n
}

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

// productVerified reports whether a vendor's quote for this holding is known
// to describe the product actually held.
//
// Only a treated foil can diverge. An untreated printing has one product per
// finish, so every vendor's bucket refers to the same card and the question
// does not arise. A ripple or surge printing is still one Scryfall id, so all
// three vendors file their foil price under the same key while not
// necessarily selling the same thing under it — which is how a comp sheet
// ends up reporting 85% "disagreement" between a $4.61 ripple and a $26.69
// something-else.
//
// The answer comes from the identifiers MTGJSON publishes, not from an
// opinion about vendors. TCGplayer and Card Kingdom both identify their
// products, so either way the printing splits, the price we hold is the
// treated one: where TCGplayer sells the treatment separately hoard fetches
// that listing (the feed carries no foil series for those printings at all,
// verified across all 312 in a live hoard), and where it does not, the
// ordinary foil listing is itself the treated foil. Card Kingdom publishes
// one foil id per printing and no alternative-foil variant, so its foil
// bucket is the treated foil by construction. Manapool publishes no
// identifier of any kind, so its quote can never be tied to a product.
//
// Hence the check is "has the feed been asked about this printing", not "is
// an id present" — an absent id is a real answer (no split product), while an
// unread set file is a gap. If MTGJSON ever adds a Manapool id, or Card
// Kingdom starts splitting treated foils, this follows the data.
// The treatment describes the printing's *foil* finish, so only a foil-priced
// holding is ambiguous. The nonfoil copy of a ripple-tagged printing is a plain
// card with one product per vendor, and suppressing its quotes would drop good
// figures for a question that was never asked.
func productVerified(provider string, o store.OwnedFinish) bool {
	if o.Treatment == "" || !scryfall.PricedAsFoil(o.Finish) {
		return true
	}
	switch provider {
	case MarketProvider, "cardkingdom":
		return o.VendorIDsKnown
	default:
		return false
	}
}

// AssessComp reduces one card's quotes to the per-vendor comp sheet for
// the finish actually owned — the same finish translation as Assess.
//
// A vendor whose product cannot be matched to the copy held is dropped rather
// than shown: every Has* stays false, so the renderers' existing dash covers
// it and the spreads, Low, and the two-vendor gate all compute over the
// figures that do describe the same card. A comp is a comparison, so a price
// for the wrong product is worse than no price at all.
func AssessComp(o store.OwnedFinish, qs []mtgjson.Quote) Comp {
	c := Comp{Card: o}
	finish := quoteFinish(o, qs)

	for _, q := range qs {
		if q.Finish != finish || q.Price <= 0 {
			continue
		}
		if !productVerified(q.Provider, o) {
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

// hasFinish reports whether any quote names this finish, which is how an
// etched holding tells "the vendors price the etched product" from "the feed
// only knows this card as a foil".
func hasFinish(qs []mtgjson.Quote, finish string) bool {
	for _, q := range qs {
		if q.Finish == finish && q.Price > 0 {
			return true
		}
	}
	return false
}

// listingOutlierRatio matches the pricing layer's troll-listing guard: a
// marketplace's "lowest ask" can be a joke listing (a $7,362,059.74
// Legion Loyalty, observed live), and a comp sheet quoting it beside the
// $2.49 the card actually sells for is noise wearing a money column.
const listingOutlierRatio = 20

// trollListing is the clamp itself, shared by the comp sheet and Assess so
// the two tables the market command renders can never disagree about which
// quotes are jokes. A lone figure is trusted — with one voice there is
// nothing to compare — hence the figure count in the signature.
func trollListing(price, cheapest float64, figures int) bool {
	return figures > 1 && cheapest > 0 && price > cheapest*listingOutlierRatio
}

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
		if trollListing(*f.p, cheapest, len(present)) {
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
