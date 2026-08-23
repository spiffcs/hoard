package market

import (
	"github.com/spiffcs/hoard/internal/finish"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

func ownedFoil(name string) store.OwnedFinish {
	return store.OwnedFinish{Name: name, SetCode: "m3c", CollectorNumber: "171",
		Finish: finish.Foil, Copies: 1, Value: 2.49}
}

func q(provider, kind string, fin finish.Finish, price float64) mtgjson.Quote {
	return mtgjson.Quote{Provider: provider, Kind: kind, Finish: fin, Price: price}
}

func TestAssessAnchorsOnMarket(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 3.20),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 2.49),
		q("manapool", mtgjson.Retail, finish.Foil, 138518.78),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 0.75),
	}
	op, usable := Assess(ownedFoil("Legion Loyalty"), qs)

	if usable != 2 {
		t.Errorf("usable retail = %d, want 2 — the troll ask is not a vendor", usable)
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

func TestAssessDropsTrollListings(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 7362059.74),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 2.49),
		q("manapool", mtgjson.Retail, finish.Foil, 3.00),
	}
	op, usable := Assess(ownedFoil("Legion Loyalty"), qs)
	if op.HasMarket {
		t.Errorf("market = %v, want the polluted anchor dropped", op.Market)
	}
	if usable != 2 {
		t.Errorf("usable retail = %d, want 2", usable)
	}
	if op.BuyAt != 2.49 || op.BuyFrom != "cardkingdom" {
		t.Errorf("buy = %v from %q, want the cheapest sane ask", op.BuyAt, op.BuyFrom)
	}

	lone, usable := Assess(ownedFoil("Legion Loyalty"), []mtgjson.Quote{
		q("manapool", mtgjson.Retail, finish.Foil, 500),
	})
	if usable != 1 || !lone.HasRetail || lone.BuyAt != 500 {
		t.Errorf("lone figure = %+v (usable %d), want trusted", lone, usable)
	}
}

func TestAssessWithoutMarketAnchor(t *testing.T) {
	qs := []mtgjson.Quote{
		q("cardkingdom", mtgjson.Retail, finish.Foil, 4.49),
		q("manapool", mtgjson.Retail, finish.Foil, 41.68),
	}
	op, _ := Assess(ownedFoil("Siege-Gang Lieutenant"), qs)
	if op.HasMarket {
		t.Errorf("market = %v, want none without a tcgplayer quote", op.Market)
	}
	if op.BuyAt != 4.49 {
		t.Errorf("buy = %v, want the cheapest ask", op.BuyAt)
	}
}

func TestAssessUsesOnlyTheOwnedFinish(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Nonfoil, 0.42),
		q("cardkingdom", mtgjson.Retail, finish.Nonfoil, 0.99),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 2.49),
		q("cardkingdom", mtgjson.Buylist, finish.Nonfoil, 0.10),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 0.75),
	}
	op, usable := Assess(ownedFoil("Legion Loyalty"), qs)
	if usable != 1 || op.BuyAt != 2.49 {
		t.Errorf("buy = %v (%d usable), want only the foil retail 2.49", op.BuyAt, usable)
	}
	if op.SellAt != 0.75 {
		t.Errorf("sell = %v, want the foil buylist 0.75", op.SellAt)
	}

	normal := ownedFoil("Legion Loyalty")
	normal.Finish = finish.Nonfoil
	op, usable = Assess(normal, qs)
	if usable != 2 || op.BuyAt != 0.99 || op.SellAt != 0.10 {
		t.Errorf("non-foil: buy %v sell %v (%d usable), want 0.99 / 0.10",
			op.BuyAt, op.SellAt, usable)
	}
}

func TestAssessIdentifiesRealArbitrage(t *testing.T) {
	o := ownedFoil("Ugin's Labyrinth")
	o.Finish = finish.Nonfoil
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Nonfoil, 14.43),
		q("tcgplayer", mtgjson.Ask, finish.Nonfoil, 14.43),
		q("cardkingdom", mtgjson.Buylist, finish.Nonfoil, 16.50),
	}
	op, _ := Assess(o, qs)
	if !op.HasBuy || op.Profit() < 2.06 || op.Profit() > 2.08 {
		t.Errorf("profit = %v, want about 2.07", op.Profit())
	}
	if op.Liquidity() <= 1 {
		t.Errorf("liquidity = %v, want above 1 when a shop pays over the sales price", op.Liquidity())
	}
}

func TestAssessWithNoRetailIsSkipped(t *testing.T) {
	op, usable := Assess(ownedFoil("Unquoted"), []mtgjson.Quote{
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 0.75),
	})
	if op.HasRetail || usable != 0 {
		t.Errorf("op = %+v (%d usable), want nothing usable", op, usable)
	}
}

func mk(name string, market, buy, sell float64) Opportunity {
	return Opportunity{
		Card:      store.OwnedFinish{Name: name, Copies: 1},
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
		mk("profit-small", 10, 10, 12),
		mk("profit-big", 8, 2, 20),
		mk("liquid", 10, 10, 9),
		mk("illiquid", 10, 10, 1),
		mk("under-big", 10, 2, 0),
		mk("under-small", 10, 7, 0),
		mk("flat", 5, 5, 0),
		mk("no-anchor", 0, 1, 0.9),
	}}

	byKind := map[Kind][]string{}
	for _, sec := range Sections(res, 10) {
		for _, o := range sec.Rows {
			byKind[sec.Kind] = append(byKind[sec.Kind], o.Card.Name)
		}
	}

	if got := byKind[KindProfit]; len(got) != 2 || got[0] != "profit-big" {
		t.Errorf("arbitrage = %v, want the biggest profit first", got)
	}

	for _, name := range byKind[KindLiquid] {
		if strings.HasPrefix(name, "profit-") {
			t.Errorf("liquid section = %v, want no profitable rows", byKind[KindLiquid])
		}
	}
	if got := byKind[KindLiquid]; len(got) != 1 || got[0] != "liquid" {
		t.Errorf("liquid = %v, want only the row above the 70%% floor", got)
	}

	if got := byKind[KindBelowMarket]; len(got) != 3 ||
		got[0] != "under-big" || got[1] != "profit-big" || got[2] != "under-small" {
		t.Errorf("below market = %v, want the deepest discounts in order", got)
	}
}

func TestLowballsRankTheWorstOffersFirst(t *testing.T) {
	res := Result{Opportunities: []Opportunity{
		mk("liquid", 10, 10, 9),
		mk("middle", 10, 10, 6),
		mk("edge", 10, 10, 5),
		mk("bad", 10, 10, 4),
		mk("worse", 10, 10, 1),
		mk("no-bid", 10, 10, 0),
		mk("no-anchor", 0, 10, 1),
		mk("profitable", 10, 2, 20),
	}}

	var got []string
	for _, o := range Lowballs(res, 10) {
		got = append(got, o.Card.Name)
	}
	if len(got) != 2 || got[0] != "worse" || got[1] != "bad" {
		t.Errorf("lowballs = %v, want the worst offer first and nothing else", got)
	}
}

func TestLowballsExcludeCardsWithNoBid(t *testing.T) {
	res := Result{Opportunities: []Opportunity{mk("unbid", 10, 10, 0)}}
	if got := Lowballs(res, 10); len(got) != 0 {
		t.Errorf("lowballs = %v, want no rows for an absent offer", got)
	}
}

func TestLowballIsNotASection(t *testing.T) {
	for _, k := range Kinds {
		if k == KindLowball {
			t.Fatal("KindLowball must stay out of Kinds: the browser indexes [3] state by Kind")
		}
	}
	res := Result{Opportunities: []Opportunity{mk("bad", 10, 10, 1)}}
	for _, sec := range Sections(res, 10) {
		if len(sec.Rows) != 0 {
			t.Errorf("%s section = %d rows, want a lowball to reach no section", sec.Kind, len(sec.Rows))
		}
	}
}

func TestLowballGradeRunsOppositeToLiquidity(t *testing.T) {

	if got := LowballGrade(0.5); got != 0 {
		t.Errorf("LowballGrade(0.5) = %v, want 0 at the ceiling", got)
	}
	if got := LowballGrade(0.10); got != 1 {
		t.Errorf("LowballGrade(0.10) = %v, want 1 at the floor", got)
	}
	if got := LowballGrade(0.01); got != 1 {
		t.Errorf("LowballGrade(0.01) = %v, want the ramp clamped below the floor", got)
	}
	if a, b := LowballGrade(0.2), LowballGrade(0.4); a <= b {
		t.Errorf("LowballGrade(0.2)=%v must exceed LowballGrade(0.4)=%v", a, b)
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
		"cheap": {q("a", mtgjson.Retail, finish.Foil, 0.20), q("b", mtgjson.Retail, finish.Foil, 1.99)},
		"dear":  {q("a", mtgjson.Retail, finish.Foil, 10), q("b", mtgjson.Retail, finish.Foil, 40)},
	}
	res := Collect(owned, quotes, 1.0)
	if len(res.Opportunities) != 1 || res.Opportunities[0].Card.Name != "dear" {
		t.Errorf("opportunities = %+v, want only the card above the floor", res.Opportunities)
	}

	if res.Compared != 2 {
		t.Errorf("Compared = %d, want 2", res.Compared)
	}
}

func TestAssessCompBuildsTheSheet(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 3.20),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 2.49),
		q("manapool", mtgjson.Retail, finish.Foil, 4.10),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 0.75),
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

func TestAssessCompUsesOnlyTheOwnedFinish(t *testing.T) {
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Nonfoil, 0.42),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 2.49),
		q("cardkingdom", mtgjson.Buylist, finish.Nonfoil, 0.10),
	}
	c := AssessComp(ownedFoil("Legion Loyalty"), qs)
	if c.HasMarket || c.Low != 2.49 || c.HasBuylist {
		t.Errorf("comp read across finishes: %+v", c)
	}
}

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

func TestMarkupGrade(t *testing.T) {
	if g := MarkupGrade(0); g != 0 {
		t.Errorf("MarkupGrade(0) = %v, want the green end", g)
	}
	if g := MarkupGrade(-0.5); g != 0 {
		t.Errorf("MarkupGrade(-0.5) = %v, want negative spreads pinned green", g)
	}
	if g := MarkupGrade(0.5); g != 0.5 {
		t.Errorf("MarkupGrade(0.5) = %v, want the linear midpoint", g)
	}
	if g := MarkupGrade(1.2); g != 1 {
		t.Errorf("MarkupGrade(1.2) = %v, want clamped at full red", g)
	}
}

func TestCollectBuildsComps(t *testing.T) {
	cheap := ownedFoil("Penny Card")
	cheap.ScryfallID, cheap.Value = "penny", 0.30
	rich := ownedFoil("Rich Card")
	rich.ScryfallID, rich.Value = "rich", 90
	lone := ownedFoil("One Vendor")
	lone.ScryfallID, lone.Value = "lone", 50

	quotes := map[string][]mtgjson.Quote{
		"penny": {q("tcgplayer", mtgjson.Retail, finish.Foil, 0.30),
			q("cardkingdom", mtgjson.Retail, finish.Foil, 0.35)},
		"rich": {q("tcgplayer", mtgjson.Retail, finish.Foil, 80),
			q("cardkingdom", mtgjson.Retail, finish.Foil, 85),
			q("cardkingdom", mtgjson.Buylist, finish.Foil, 60)},
		"lone": {q("cardkingdom", mtgjson.Retail, finish.Foil, 55)},
	}
	res := Collect([]store.OwnedFinish{cheap, rich, lone}, quotes, 1.0)

	if len(res.Comps) != 1 || res.Comps[0].Card.ScryfallID != "rich" {
		t.Fatalf("comps = %+v, want only the rich two-vendor card", res.Comps)
	}
	if res.Compared != 2 {
		t.Errorf("Compared = %d, want 2 (the gate is unchanged)", res.Compared)
	}

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

func TestCompSaleSpread(t *testing.T) {
	cases := []struct {
		name string
		c    Comp
		want float64
		ok   bool
	}{
		{"two prices", Comp{Market: 8, HasMarket: true, CK: 10, HasCK: true}, 0.2, true},
		{"three prices span", Comp{Market: 10, HasMarket: true, Manapool: 5, HasManapool: true, CK: 9, HasCK: true}, 0.5, true},
		{"agreement", Comp{Market: 10, HasMarket: true, Manapool: 10, HasManapool: true}, 0, true},
		{"one price", Comp{Market: 10, HasMarket: true}, 0, false},
		{"no prices", Comp{}, 0, false},
	}
	for _, tc := range cases {
		got, ok := tc.c.SaleSpread()
		if ok != tc.ok || (ok && (got < tc.want-1e-9 || got > tc.want+1e-9)) {
			t.Errorf("%s: SaleSpread = %v,%v; want %v,%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}

	if g := SaleSpreadGrade(0.05); g != 0 {
		t.Errorf("grade(0.05) = %v, want the green end", g)
	}
	if g := SaleSpreadGrade(0.50); g != 1 {
		t.Errorf("grade(0.50) = %v, want saturated", g)
	}
}

func TestAssessCompDropsTrollListings(t *testing.T) {
	owned := store.OwnedFinish{ScryfallID: "legion", Name: "Legion Loyalty", Finish: finish.Foil}
	qs := []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 2.10},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 2.49},
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 7362059.74},
	}
	c := AssessComp(owned, qs)
	if c.HasManapool {
		t.Errorf("manapool = %v, want the troll listing dropped", c.Manapool)
	}
	if !c.HasMarket || !c.HasCK {
		t.Errorf("sane figures must survive: %+v", c)
	}
	if c.Low != 2.10 || c.LowFrom != "tcgplayer" {
		t.Errorf("low = %v from %q, want 2.10 from tcgplayer", c.Low, c.LowFrom)
	}

	lone := AssessComp(owned, []mtgjson.Quote{
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 500},
	})
	if !lone.HasManapool || lone.Manapool != 500 {
		t.Errorf("lone figure = %+v, want trusted", lone)
	}
}

func TestAssessCompDropsUnverifiableTreatedQuotes(t *testing.T) {
	ripple := store.OwnedFinish{
		ScryfallID: "urza", Name: "Urza's Tower", Finish: finish.Foil,
		Treatment: "ripple", VendorIDsKnown: true,
	}
	qs := []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 4.61},
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 26.69},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 3.99},
	}
	c := AssessComp(ripple, qs)
	if c.HasManapool {
		t.Errorf("manapool = %v, want dropped: no product id to match it to the ripple", c.Manapool)
	}
	if !c.HasMarket || !c.HasCK {
		t.Errorf("the identified vendors must survive: %+v", c)
	}
	if c.Low != 3.99 || c.LowFrom != "cardkingdom" {
		t.Errorf("low = %v from %q, want the unverified figure excluded", c.Low, c.LowFrom)
	}
	got, ok := c.SaleSpread()
	if !ok || got > 0.20 {
		t.Errorf("sale spread = %v (ok %v), want the real TCG-vs-CK gap, not the bogus 85%%", got, ok)
	}
}

func TestAssessCompKeepsManapoolOnPlainFoils(t *testing.T) {
	plain := store.OwnedFinish{ScryfallID: "legion", Name: "Legion Loyalty", Finish: finish.Foil}
	c := AssessComp(plain, []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 3.20},
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 4.10},
	})
	if !c.HasManapool || c.Manapool != 4.10 {
		t.Errorf("manapool = %+v, want kept: an untreated printing is unambiguous", c)
	}
}

func TestAssessCompWaitsForVendorIDs(t *testing.T) {
	unasked := store.OwnedFinish{
		ScryfallID: "urza", Name: "Urza's Tower", Finish: finish.Foil,
		Treatment: "ripple", VendorIDsKnown: false,
	}
	c := AssessComp(unasked, []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 4.61},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 3.99},
	})
	if c.HasMarket || c.HasCK || c.HasManapool {
		t.Errorf("comp = %+v, want nothing claimed before the ids are known", c)
	}
	if _, ok := c.SaleSpread(); ok {
		t.Error("sale spread defined with no verified figures")
	}
}

func TestAssessCompPrefersTheEtchedBucket(t *testing.T) {
	owned := store.OwnedFinish{ScryfallID: "kenrith", Name: "Kenrith", Finish: finish.Etched}
	c := AssessComp(owned, []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 9.99},
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Etched, Price: 24.50},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Etched, Price: 27.99},
	})
	if !c.HasMarket || c.Market != 24.50 {
		t.Errorf("market = %v, want the etched product's price", c.Market)
	}
	if !c.HasCK || c.CK != 27.99 {
		t.Errorf("ck = %v, want the etched product's price", c.CK)
	}

	fallback := AssessComp(owned, []mtgjson.Quote{
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 9.99},
	})
	if !fallback.HasMarket || fallback.Market != 9.99 {
		t.Errorf("fallback = %+v, want the foil bucket when there is no etched one", fallback)
	}
}

func TestAssessDropsUnverifiableTreatedQuotes(t *testing.T) {
	ripple := store.OwnedFinish{
		ScryfallID: "urza", Name: "Urza's Tower", Finish: finish.Foil,
		Treatment: "ripple", VendorIDsKnown: true,
	}
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 4.61),
		q("manapool", mtgjson.Retail, finish.Foil, 26.69),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 3.99),
	}
	op, retail := Assess(ripple, qs)

	if retail != 2 {
		t.Errorf("retail count = %d, want 2: the unidentifiable quote is not a vendor that priced this product", retail)
	}
	if op.BuyAt != 3.99 || op.BuyFrom != "cardkingdom" {
		t.Errorf("buy = %v from %q, want the unverified figure excluded", op.BuyAt, op.BuyFrom)
	}

	if c := AssessComp(ripple, qs); c.Low != op.BuyAt {
		t.Errorf("comp low %v != opportunity buy %v: the sheet and the sections disagree", c.Low, op.BuyAt)
	}
}

func TestAssessKeepsManapoolOnPlainFoils(t *testing.T) {
	plain := store.OwnedFinish{ScryfallID: "legion", Name: "Legion Loyalty", Finish: finish.Foil}
	op, retail := Assess(plain, []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 3.20),
		q("manapool", mtgjson.Retail, finish.Foil, 2.10),
	})
	if retail != 2 || op.BuyAt != 2.10 || op.BuyFrom != "manapool" {
		t.Errorf("op = %+v (retail %d), want manapool kept: an untreated printing is unambiguous", op, retail)
	}
}

func TestAssessWaitsForVendorIDs(t *testing.T) {
	unasked := store.OwnedFinish{
		ScryfallID: "urza", Name: "Urza's Tower", Finish: finish.Foil,
		Treatment: "ripple", VendorIDsKnown: false,
	}
	op, retail := Assess(unasked, []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 4.61),
		q("cardkingdom", mtgjson.Retail, finish.Foil, 3.99),
	})
	if retail != 0 || op.HasRetail || op.HasMarket {
		t.Errorf("op = %+v (retail %d), want nothing claimed before the ids are known", op, retail)
	}
}

func TestTreatmentDoesNotSuppressNonfoilQuotes(t *testing.T) {
	nonfoil := store.OwnedFinish{
		ScryfallID: "urza", Name: "Urza's Tower", Finish: finish.Nonfoil,
		Treatment: "ripple", VendorIDsKnown: false,
	}
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Nonfoil, 0.85),
		q("manapool", mtgjson.Retail, finish.Nonfoil, 0.60),
	}
	op, retail := Assess(nonfoil, qs)
	if retail != 2 || op.BuyAt != 0.60 {
		t.Errorf("op = %+v (retail %d), want both nonfoil quotes kept", op, retail)
	}
	if c := AssessComp(nonfoil, qs); !c.HasManapool || !c.HasMarket {
		t.Errorf("comp = %+v, want both nonfoil quotes kept", c)
	}
}

func TestAssessPrefersTheEtchedBucket(t *testing.T) {
	owned := store.OwnedFinish{ScryfallID: "kenrith", Name: "Kenrith", Finish: finish.Etched}
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, finish.Foil, 9.99),
		q("tcgplayer", mtgjson.Retail, finish.Etched, 24.50),
		q("cardkingdom", mtgjson.Retail, finish.Etched, 27.99),
	}
	op, retail := Assess(owned, qs)
	if !op.HasMarket || op.Market != 24.50 {
		t.Errorf("market = %v, want the etched product's price", op.Market)
	}
	if retail != 2 || op.BuyAt != 27.99 {
		t.Errorf("op = %+v (retail %d), want only etched quotes", op, retail)
	}

	fallback, _ := Assess(owned, []mtgjson.Quote{q("tcgplayer", mtgjson.Retail, finish.Foil, 9.99)})
	if !fallback.HasMarket || fallback.Market != 9.99 {
		t.Errorf("fallback = %+v, want the foil bucket when there is no etched one", fallback)
	}
}
