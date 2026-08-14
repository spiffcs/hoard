package market

import (
	"cmp"
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type Comp struct {
	Card store.OwnedFinish

	Market      float64
	CK          float64
	Manapool    float64
	HasMarket   bool
	HasCK       bool
	HasManapool bool

	Low     float64
	LowFrom string

	Buylist    float64
	BuylistTo  string
	HasBuylist bool
}

func (c Comp) Printing() string {
	return ui.Printing(c.Card.SetCode, c.Card.CollectorNumber)
}

func (c Comp) HasSpread() bool { return c.HasBuylist && c.Low > 0 }

func (c Comp) Figures() int {
	n := 0
	for _, has := range []bool{c.HasMarket, c.HasManapool, c.HasCK} {
		if has {
			n++
		}
	}
	return n
}

func (c Comp) Spread() float64 { return 1 - c.Buylist/c.Low }

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

const (
	saleSpreadTight = 0.05
	saleSpreadWide  = 0.50
)

func SaleSpreadGrade(s float64) float64 { return grade(s, saleSpreadTight, saleSpreadWide) }

func MarkupGrade(s float64) float64 { return min(max(s, 0), 1) }

const (
	CompsTitle = "COMPS"
	CompsNote  = "a list comparing vendor prices"
)

func productVerified(provider string, o store.OwnedFinish) bool {
	if o.Treatment == "" || !o.Finish.UsesFoilPricing() {
		return true
	}
	switch provider {
	case MarketProvider, "cardkingdom":
		return o.VendorIDsKnown
	default:
		return false
	}
}

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

func hasFinish(qs []mtgjson.Quote, fin finish.Finish) bool {
	for _, q := range qs {
		if q.Finish == fin && q.Price > 0 {
			return true
		}
	}
	return false
}

func nonPrice(price float64, figures []float64) bool {
	return mtgjson.NonPrice(price, figures)
}

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
	c.Low, c.LowFrom = 0, ""
	names := []string{MarketProvider, "cardkingdom", "manapool"}
	for i, f := range figs {
		if !*f.has {
			continue
		}
		if nonPrice(*f.p, present) {
			*f.p, *f.has = 0, false
			continue
		}
		if c.LowFrom == "" || *f.p < c.Low {
			c.Low, c.LowFrom = *f.p, names[i]
		}
	}
}

func TopComps(comps []Comp, limit int) []Comp {
	if limit <= 0 || limit > len(comps) {
		limit = len(comps)
	}
	return append([]Comp(nil), comps[:limit]...)
}

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
		return strings.Compare(a.Card.Finish.String(), b.Card.Finish.String())
	})
}
