package market

import (
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

func ownedFoil(name string) store.OwnedFinish {
	return store.OwnedFinish{Name: name, SetCode: "m3c", CollectorNumber: "171",
		Finish: "foil", Copies: 1, Value: 2.49}
}

func q(provider, kind, finish string, price float64) mtgjson.Quote {
	return mtgjson.Quote{Provider: provider, Kind: kind, Finish: finish, Price: price}
}

// The listing this guard exists for. Manapool quotes Legion Loyalty foil at
// $138,518.78 against Card Kingdom's $2.49; it is real data in MTGJSON, and a
// naive "widest spread first" ranking puts it at the top of the first screen.
// The listing this design exists for. Manapool quotes Legion Loyalty foil at
// $138,518.78 against Card Kingdom's $2.49 — real data in MTGJSON. Anchored
// on the sales price, a lone high ask simply never becomes a row: only
// asks *below* what cards actually sell for are interesting.
func TestAssessAnchorsOnMarket(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "foil", 3.20),
		q("cardkingdom", mtgjson.Retail, "foil", 2.49),
		q("manapool", mtgjson.Retail, "foil", 138518.78),
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	}
	op, usable := Assess(ownedFoil("Legion Loyalty"), qs)

	if usable != 3 {
		t.Errorf("usable retail = %d, want 3", usable)
	}
	if !op.HasMarket || op.Market != 3.20 {
		t.Errorf("market = %v (has %v), want the tcgplayer sales price", op.Market, op.HasMarket)
	}
	if op.BuyAt != 2.49 || op.BuyFrom != "cardkingdom" {
		t.Errorf("buy = %v from %q, want the cheapest ask", op.BuyAt, op.BuyFrom)
	}
	if op.SellAt != 0.75 {
		t.Errorf("sell = %v, want 0.75", op.SellAt)
	}
}

// Without a tcgplayer quote there is no anchor: the row can still say what
// is buyable and what a shop pays, but the market-relative sections skip it.
func TestAssessWithoutMarketAnchor(t *testing.T) {
	qs := []mtgjson.Quote{
		q("cardkingdom", mtgjson.Retail, "foil", 4.49),
		q("manapool", mtgjson.Retail, "foil", 41.68),
	}
	op, _ := Assess(ownedFoil("Siege-Gang Lieutenant"), qs)
	if op.HasMarket {
		t.Errorf("market = %v, want none without a tcgplayer quote", op.Market)
	}
	if op.BuyAt != 4.49 {
		t.Errorf("buy = %v, want the cheapest ask", op.BuyAt)
	}
}

// Quotes are per finish: what a shop pays for the non-foil says nothing about
// the foil, and reading across the two would invent a price.
func TestAssessUsesOnlyTheOwnedFinish(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "normal", 0.42),
		q("cardkingdom", mtgjson.Retail, "normal", 0.99),
		q("cardkingdom", mtgjson.Retail, "foil", 2.49),
		q("cardkingdom", mtgjson.Buylist, "normal", 0.10),
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	}
	op, usable := Assess(ownedFoil("Legion Loyalty"), qs)
	if usable != 1 || op.BuyAt != 2.49 {
		t.Errorf("buy = %v (%d usable), want only the foil retail 2.49", op.BuyAt, usable)
	}
	if op.SellAt != 0.75 {
		t.Errorf("sell = %v, want the foil buylist 0.75", op.SellAt)
	}

	// The same card owned in non-foil reads the other quotes instead.
	normal := ownedFoil("Legion Loyalty")
	normal.Finish = "nonfoil"
	op, usable = Assess(normal, qs)
	if usable != 2 || op.BuyAt != 0.42 || op.SellAt != 0.10 {
		t.Errorf("non-foil: buy %v sell %v (%d usable), want 0.42 / 0.10",
			op.BuyAt, op.SellAt, usable)
	}
}

// Buylist above the cheapest retail is the only unambiguous signal here.
func TestAssessIdentifiesRealArbitrage(t *testing.T) {
	o := ownedFoil("Ugin's Labyrinth")
	o.Finish = "nonfoil"
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "normal", 14.43),
		q("cardkingdom", mtgjson.Buylist, "normal", 16.50),
	}
	op, _ := Assess(o, qs)
	if !op.HasBuy || op.Profit() < 2.06 || op.Profit() > 2.08 {
		t.Errorf("profit = %v, want about 2.07", op.Profit())
	}
	if op.Liquidity() <= 1 {
		t.Errorf("liquidity = %v, want above 1 when a shop pays over the sales price", op.Liquidity())
	}
}

// A card nobody quotes must not reach the report at all.
func TestAssessWithNoRetailIsSkipped(t *testing.T) {
	op, usable := Assess(ownedFoil("Unquoted"), []mtgjson.Quote{
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	})
	if op.HasRetail || usable != 0 {
		t.Errorf("op = %+v (%d usable), want nothing usable", op, usable)
	}
}

// mk builds an opportunity with just the fields a ranking test reads:
// the sales-price anchor, the cheapest ask, and the best buylist.
func mk(name string, market, buy, sell float64) Opportunity {
	return Opportunity{
		Card:      store.OwnedFinish{Name: name},
		Market:    market,
		BuyAt:     buy,
		SellAt:    sell,
		HasMarket: market > 0,
		HasRetail: true,
		HasBuy:    sell > 0,
	}
}

func TestSectionsRankEachQuestionSeparately(t *testing.T) {
	res := Result{Opportunities: []Opportunity{
		mk("profit-small", 10, 10, 12), // +$2 profit
		mk("profit-big", 8, 2, 20),     // +$18 profit (also 75% below market)
		mk("liquid", 10, 10, 9),        // no profit, buylist pays 90% of sales
		mk("illiquid", 10, 10, 1),      // no profit, 10% — under the floor
		mk("under-big", 10, 2, 0),      // asking 80% below the sales price
		mk("under-small", 10, 7, 0),    // 30% below
		mk("flat", 5, 5, 0),            // nothing to say
		mk("no-anchor", 0, 1, 0.9),     // no market: excluded from anchored sections
	}}

	byKind := map[Kind][]string{}
	for _, sec := range Sections(res, 10) {
		for _, o := range sec.Rows {
			byKind[sec.Kind] = append(byKind[sec.Kind], o.Card.Name)
		}
	}

	// Real arbitrage, ranked by absolute profit rather than percentage.
	if got := byKind[KindProfit]; len(got) != 2 || got[0] != "profit-big" {
		t.Errorf("arbitrage = %v, want the biggest profit first", got)
	}
	// Easy to sell excludes anything already profitable, so a card cannot
	// appear in both of the first two sections and dilute either.
	for _, name := range byKind[KindLiquid] {
		if strings.HasPrefix(name, "profit-") {
			t.Errorf("liquid section = %v, want no profitable rows", byKind[KindLiquid])
		}
	}
	if got := byKind[KindLiquid]; len(got) != 1 || got[0] != "liquid" {
		t.Errorf("liquid = %v, want only the row above the 70%% floor", got)
	}
	// Below market lists real asks under the sales price, deepest discount
	// first; a flat ask and an unanchored row say nothing.
	if got := byKind[KindBelowMarket]; len(got) != 3 ||
		got[0] != "under-big" || got[1] != "profit-big" || got[2] != "under-small" {
		t.Errorf("below market = %v, want the deepest discounts in order", got)
	}
}

func TestSectionsRespectTheLimit(t *testing.T) {
	var res Result
	for i := range 20 {
		res.Opportunities = append(res.Opportunities, mk("c"+strconv.Itoa(i), 2, 1, 5))
	}
	for _, sec := range Sections(res, 3) {
		if len(sec.Rows) > 3 {
			t.Errorf("%s has %d rows, want at most 3", sec.Kind, len(sec.Rows))
		}
	}
}

// Rows flattens the sections in reading order, so a scrolling list leads with
// real arbitrage and still says which question each row answers.
func TestRowsFlattenInSectionOrder(t *testing.T) {
	res := Result{Opportunities: []Opportunity{
		mk("profit", 8, 2, 20),
		mk("liquid", 10, 10, 9),
	}}
	rows := Rows(res, 10)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	if rows[0].Kind != KindProfit || rows[0].Card.Name != "profit" {
		t.Errorf("first row = %+v, want the arbitrage row", rows[0])
	}
	var seen []Kind
	for _, r := range rows {
		if len(seen) == 0 || seen[len(seen)-1] != r.Kind {
			seen = append(seen, r.Kind)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Errorf("kinds out of order: %v", seen)
		}
	}
}

func TestCollectFiltersByPrice(t *testing.T) {
	owned := []store.OwnedFinish{ownedFoil("cheap"), ownedFoil("dear")}
	owned[0].ScryfallID, owned[1].ScryfallID = "cheap", "dear"
	quotes := map[string][]mtgjson.Quote{
		"cheap": {q("a", mtgjson.Retail, "foil", 0.20), q("b", mtgjson.Retail, "foil", 1.99)},
		"dear":  {q("a", mtgjson.Retail, "foil", 10), q("b", mtgjson.Retail, "foil", 40)},
	}
	res := Collect(owned, quotes, 1.0)
	if len(res.Opportunities) != 1 || res.Opportunities[0].Card.Name != "dear" {
		t.Errorf("opportunities = %+v, want only the card above the floor", res.Opportunities)
	}
	// Both had two vendors, so both counted as compared even though one was
	// filtered out of the results.
	if res.Compared != 2 {
		t.Errorf("Compared = %d, want 2", res.Compared)
	}
}

// AssessComp builds the full sheet: each vendor's own column, the cheapest
// ask as Low, and cardkingdom's bid on the buylist side.
func TestAssessCompBuildsTheSheet(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "foil", 3.20),
		q("cardkingdom", mtgjson.Retail, "foil", 2.49),
		q("manapool", mtgjson.Retail, "foil", 4.10),
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	}
	c := AssessComp(ownedFoil("Legion Loyalty"), qs)
	if !c.HasMarket || c.Market != 3.20 {
		t.Errorf("market = %v (has %v)", c.Market, c.HasMarket)
	}
	if !c.HasCK || c.CK != 2.49 || !c.HasManapool || c.Manapool != 4.10 {
		t.Errorf("vendor columns = ck %v mp %v", c.CK, c.Manapool)
	}
	if c.Low != 2.49 || c.LowFrom != "cardkingdom" {
		t.Errorf("low = %v from %q, want the cheapest ask", c.Low, c.LowFrom)
	}
	if !c.HasBuylist || c.Buylist != 0.75 || c.BuylistTo != "cardkingdom" {
		t.Errorf("buylist = %v to %q", c.Buylist, c.BuylistTo)
	}
}

// The comp sheet reads only the owned finish, like everything else.
func TestAssessCompUsesOnlyTheOwnedFinish(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "normal", 0.42),
		q("cardkingdom", mtgjson.Retail, "foil", 2.49),
		q("cardkingdom", mtgjson.Buylist, "normal", 0.10),
	}
	c := AssessComp(ownedFoil("Legion Loyalty"), qs)
	if c.HasMarket || c.Low != 2.49 || c.HasBuylist {
		t.Errorf("comp read across finishes: %+v", c)
	}
}

// Spread is (low − bid)/low, defined only when both sides exist; a bid
// above the low ask goes negative — genuine arbitrage, maximally real.
func TestCompSpreadMath(t *testing.T) {
	c := Comp{Low: 26.00, Buylist: 18.20, HasBuylist: true}
	if !c.HasSpread() {
		t.Fatal("both sides present, spread must be defined")
	}
	if s := c.Spread(); s < 0.2999 || s > 0.3001 {
		t.Errorf("spread = %v, want 30%%", s)
	}
	neg := Comp{Low: 2.00, Buylist: 2.50, HasBuylist: true}
	if neg.Spread() >= 0 {
		t.Errorf("bid above ask should read negative, got %v", neg.Spread())
	}
	if none := (Comp{Low: 5.00}); none.HasSpread() {
		t.Error("no buylist bid means no spread")
	}
}

// The ramp anchors on the hobby's landmarks: 20% saturates green, 85%
// floors amber, and the negative-arbitrage case clamps to full green.
func TestSpreadGradeAnchors(t *testing.T) {
	if g := SpreadGrade(0.20); g != 1 {
		t.Errorf("20%% = %v, want 1", g)
	}
	if g := SpreadGrade(0.85); g != 0 {
		t.Errorf("85%% = %v, want 0", g)
	}
	if g := SpreadGrade(0.525); g < 0.49 || g > 0.51 {
		t.Errorf("midpoint = %v, want ~0.5", g)
	}
	if g := SpreadGrade(-0.10); g != 1 {
		t.Errorf("negative spread = %v, want the full green clamp", g)
	}
}

// Collect gates comps at two retail quotes, filters on the low ask, and
// ranks by value with the truncation-stable tiebreak.
func TestCollectBuildsComps(t *testing.T) {
	cheap := ownedFoil("Penny Card")
	cheap.ScryfallID, cheap.Value = "penny", 0.30
	rich := ownedFoil("Rich Card")
	rich.ScryfallID, rich.Value = "rich", 90
	lone := ownedFoil("One Vendor")
	lone.ScryfallID, lone.Value = "lone", 50

	quotes := map[string][]mtgjson.Quote{
		"penny": {q("tcgplayer", mtgjson.Retail, "foil", 0.30),
			q("cardkingdom", mtgjson.Retail, "foil", 0.35)},
		"rich": {q("tcgplayer", mtgjson.Retail, "foil", 80),
			q("cardkingdom", mtgjson.Retail, "foil", 85),
			q("cardkingdom", mtgjson.Buylist, "foil", 60)},
		"lone": {q("cardkingdom", mtgjson.Retail, "foil", 55)},
	}
	res := Collect([]store.OwnedFinish{cheap, rich, lone}, quotes, 1.0)

	if len(res.Comps) != 1 || res.Comps[0].Card.ScryfallID != "rich" {
		t.Fatalf("comps = %+v, want only the rich two-vendor card", res.Comps)
	}
	if res.Compared != 2 {
		t.Errorf("Compared = %d, want 2 (the gate is unchanged)", res.Compared)
	}

	// Without the value filter the penny card joins, ranked below.
	res = Collect([]store.OwnedFinish{cheap, rich}, quotes, 0)
	if len(res.Comps) != 2 || res.Comps[0].Card.ScryfallID != "rich" {
		t.Fatalf("comps order = %+v, want value-descending", res.Comps)
	}
}

func TestTopCompsTruncates(t *testing.T) {
	comps := []Comp{{Low: 1}, {Low: 2}, {Low: 3}}
	if got := TopComps(comps, 2); len(got) != 2 {
		t.Errorf("TopComps(2) = %d rows", len(got))
	}
	if got := TopComps(comps, 0); len(got) != 3 {
		t.Errorf("TopComps(0) = %d rows, want everything", len(got))
	}
	got := TopComps(comps, 3)
	got[0].Low = 99
	if comps[0].Low == 99 {
		t.Error("TopComps must copy, not alias")
	}
}

// Verdict classifies a comp sheet with the sections' own thresholds: a
// bid over the sales price is arbitrage, at 70% or better easy to sell,
// under that nothing.
func TestCompVerdict(t *testing.T) {
	sheet := func(market, bid float64) Comp {
		return Comp{Market: market, HasMarket: market > 0, Buylist: bid, HasBuylist: bid > 0}
	}
	cases := []struct {
		name string
		c    Comp
		kind Kind
		ok   bool
	}{
		{"bid over market", sheet(10, 11), KindProfit, true},
		{"bid at the liquid floor", sheet(10, 7), KindLiquid, true},
		{"bid under the floor", sheet(10, 6.99), 0, false},
		{"no bid", sheet(10, 0), 0, false},
		{"no market", sheet(0, 5), 0, false},
	}
	for _, tc := range cases {
		k, ok := tc.c.Verdict()
		if ok != tc.ok || (ok && k != tc.kind) {
			t.Errorf("%s: verdict = %v,%v; want %v,%v", tc.name, k, ok, tc.kind, tc.ok)
		}
	}
}
