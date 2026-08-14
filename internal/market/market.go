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

const MarketProvider = "tcgplayer"

type Opportunity struct {
	Card store.OwnedFinish

	Market    float64
	BuyAt     float64
	BuyFrom   string
	SellAt    float64
	SellTo    string
	HasMarket bool
	HasRetail bool
	HasBuy    bool
}

func (o Opportunity) Liquidity() float64 { return o.SellAt / o.Market }

func LiquidityGrade(r float64) float64 { return grade(r, liquidFloor, 1.0) }

func LowballGrade(r float64) float64 { return 1 - grade(r, lowballFloor, lowballCeiling) }

func BelowMarketGrade(b float64) float64 { return grade(b, belowMarketFloor, 0.6) }

func grade(v, lo, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	return min(max((v-lo)/(hi-lo), 0), 1)
}

func (o Opportunity) BelowMarket() float64 { return 1 - o.BuyAt/o.Market }

func (o Opportunity) Profit() float64 { return o.SellAt - o.Market }

func (o Opportunity) Printing() string {
	return ui.Printing(o.Card.SetCode, o.Card.CollectorNumber)
}

type Result struct {
	Opportunities []Opportunity

	Comps []Comp

	Compared int
}

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

			if c := AssessComp(o, qs); c.Figures() >= 2 && c.Low >= minValue {
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

func quoteFinish(o store.OwnedFinish, qs []mtgjson.Quote) finish.Finish {
	if o.Finish == finish.Etched && hasFinish(qs, finish.Etched) {
		return finish.Etched
	}
	if o.Finish.UsesFoilPricing() {
		return finish.Foil
	}
	return finish.Nonfoil
}

func Assess(o store.OwnedFinish, qs []mtgjson.Quote) (op Opportunity, retailCount int) {
	op.Card = o
	finish := quoteFinish(o, qs)

	var retail []float64
	for _, q := range qs {
		if q.Kind != mtgjson.Retail || q.Finish != finish || q.Price <= 0 {
			continue
		}
		if !productVerified(q.Provider, o) {
			continue
		}
		retail = append(retail, q.Price)
	}

	for _, q := range qs {
		if q.Finish != finish || q.Price <= 0 {
			continue
		}
		if !productVerified(q.Provider, o) {
			continue
		}
		switch q.Kind {
		case mtgjson.Retail:
			if nonPrice(q.Price, retail) {
				continue
			}
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

type Kind int

const (
	KindProfit Kind = iota

	KindLiquid

	KindBelowMarket

	KindLowball
)

func (k Kind) String() string {
	switch k {
	case KindProfit:
		return "arbitrage"
	case KindLiquid:
		return "liquid"
	case KindLowball:
		return "lowball"
	}
	return "below-market"
}

func (k Kind) Title() string {
	switch k {
	case KindProfit:
		return "ARBITRAGE"
	case KindLiquid:
		return "BUYLIST NEAR MARKET"
	case KindLowball:
		return "BUYLIST LOWBALL"
	}
	return "BELOW MARKET"
}

func (k Kind) Note() string {
	switch k {
	case KindProfit:
		return "CK buylist pays more than TCG last-sold"
	case KindLiquid:
		return "CK buylist pays at least 70% of TCG last-sold"
	case KindLowball:
		return "CK buylist pays under 50% of TCG last-sold"
	}
	return "a marketplace is asking far under tcg's last-sold price"
}

const liquidFloor = 0.7

const lowballCeiling = 0.5

const lowballFloor = 0.10

const belowMarketFloor = 0.25

var Kinds = []Kind{KindProfit, KindLiquid, KindBelowMarket}

type Section struct {
	Kind Kind
	Rows []Opportunity
}

func Sections(r Result, limit int) []Section {
	out := make([]Section, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, Section{Kind: k, Rows: top(r.Opportunities, limit, k)})
	}
	return out
}

type Row struct {
	Opportunity
	Kind Kind
}

func Rows(r Result, limit int) []Row {
	var out []Row
	for _, sec := range Sections(r, limit) {
		for _, o := range sec.Rows {
			out = append(out, Row{Opportunity: o, Kind: sec.Kind})
		}
	}
	return out
}

func Lowballs(r Result, limit int) []Opportunity {
	return top(r.Opportunities, limit, KindLowball)
}

func top(all []Opportunity, limit int, k Kind) []Opportunity {
	keep := func(o Opportunity) bool { return o.HasBuy && o.HasMarket && o.Profit() > 0 }
	order := func(a, b Opportunity) int { return cmp.Compare(b.Profit(), a.Profit()) }
	switch k {
	case KindLiquid:

		keep = func(o Opportunity) bool {
			return o.HasBuy && o.HasMarket && o.Profit() <= 0 && o.Liquidity() >= liquidFloor
		}
		order = func(a, b Opportunity) int { return cmp.Compare(b.Liquidity(), a.Liquidity()) }
	case KindLowball:

		keep = func(o Opportunity) bool {
			return o.HasBuy && o.HasMarket && o.Liquidity() < lowballCeiling
		}
		order = func(a, b Opportunity) int { return cmp.Compare(a.Liquidity(), b.Liquidity()) }
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
		return strings.Compare(a.Card.Finish.String(), b.Card.Finish.String())
	})
	return out[:min(len(out), limit)]
}
