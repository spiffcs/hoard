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

// spreadTight and spreadWide anchor the display ramp on those landmarks.
const (
	spreadTight = 0.20
	spreadWide  = 0.85
)

// SpreadGrade positions a spread on 0..1 for display gradients, high =
// good: at or under 20% saturates the green end, 85% and past sits at the
// amber floor. Inverted relative to the other grades because here tight
// is the virtue; a negative spread (a bid above the low ask — genuine
// arbitrage) clamps to 1, correctly reading as maximally real.
func SpreadGrade(s float64) float64 { return 1 - grade(s, spreadTight, spreadWide) }

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
	return c
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
