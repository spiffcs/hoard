package arbitrage

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
func TestAssessDropsUnsupportedListing(t *testing.T) {
	qs := []mtgjson.Quote{
		q("cardkingdom", mtgjson.Retail, "foil", 2.49),
		q("manapool", mtgjson.Retail, "foil", 138518.78),
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	}
	op, usable, dropped := Assess(ownedFoil("Legion Loyalty"), qs)

	if dropped != 1 {
		t.Errorf("dropped = %d, want the manapool listing discarded", dropped)
	}
	if usable != 1 {
		t.Errorf("usable retail = %d, want 1", usable)
	}
	if op.BuyAt != 2.49 || op.BuyFrom != "cardkingdom" {
		t.Errorf("buy = %v from %q, want 2.49 cardkingdom", op.BuyAt, op.BuyFrom)
	}
	// With the bogus quote gone the only survivor is also the cheapest, so there
	// is no spread left to report.
	if op.DearAt != 2.49 {
		t.Errorf("dear = %v, want the surviving quote", op.DearAt)
	}
	if op.Spread() != 0 {
		t.Errorf("spread = %v, want none once the outlier is gone", op.Spread())
	}
}

// The widest genuine disagreement measured was 9.3x. It must survive.
func TestAssessKeepsWideButRealSpread(t *testing.T) {
	qs := []mtgjson.Quote{
		q("cardkingdom", mtgjson.Retail, "foil", 4.49),
		q("manapool", mtgjson.Retail, "foil", 41.68),
	}
	op, _, dropped := Assess(ownedFoil("Siege-Gang Lieutenant"), qs)
	if dropped != 0 {
		t.Errorf("dropped %d, want a 9.3x spread kept", dropped)
	}
	if op.DearAt != 41.68 || op.DearFrom != "manapool" {
		t.Errorf("dear = %v from %q, want 41.68 manapool", op.DearAt, op.DearFrom)
	}
	if got := op.Spread(); got < 8.2 || got > 8.3 {
		t.Errorf("spread = %v, want about 8.28 (i.e. +828%%)", got)
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
	op, usable, _ := Assess(ownedFoil("Legion Loyalty"), qs)
	if usable != 1 || op.BuyAt != 2.49 {
		t.Errorf("buy = %v (%d usable), want only the foil retail 2.49", op.BuyAt, usable)
	}
	if op.SellAt != 0.75 {
		t.Errorf("sell = %v, want the foil buylist 0.75", op.SellAt)
	}

	// The same card owned in non-foil reads the other quotes instead.
	normal := ownedFoil("Legion Loyalty")
	normal.Finish = "normal"
	op, usable, _ = Assess(normal, qs)
	if usable != 2 || op.BuyAt != 0.42 || op.SellAt != 0.10 {
		t.Errorf("non-foil: buy %v sell %v (%d usable), want 0.42 / 0.10",
			op.BuyAt, op.SellAt, usable)
	}
}

// Buylist above the cheapest retail is the only unambiguous signal here.
func TestAssessIdentifiesRealArbitrage(t *testing.T) {
	o := ownedFoil("Ugin's Labyrinth")
	o.Finish = "normal"
	qs := []mtgjson.Quote{
		q("tcgplayer", mtgjson.Retail, "normal", 14.43),
		q("cardkingdom", mtgjson.Buylist, "normal", 16.50),
	}
	op, _, _ := Assess(o, qs)
	if !op.HasBuy || op.Profit() < 2.06 || op.Profit() > 2.08 {
		t.Errorf("profit = %v, want about 2.07", op.Profit())
	}
	if op.Liquidity() <= 1 {
		t.Errorf("liquidity = %v, want above 1 when a shop pays over retail", op.Liquidity())
	}
}

// A card nobody quotes must not reach the report at all.
func TestAssessWithNoRetailIsSkipped(t *testing.T) {
	op, usable, dropped := Assess(ownedFoil("Unquoted"), []mtgjson.Quote{
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	})
	if op.HasRetail || usable != 0 || dropped != 0 {
		t.Errorf("op = %+v (%d usable, %d dropped), want nothing usable", op, usable, dropped)
	}
}

// mk builds an opportunity with just the fields a ranking test reads.
func mk(name string, buy, dear, sell float64) Opportunity {
	return Opportunity{
		Card:      store.OwnedFinish{Name: name},
		BuyAt:     buy,
		DearAt:    dear,
		SellAt:    sell,
		HasRetail: true,
		HasBuy:    sell > 0,
	}
}

func TestSectionsRankEachQuestionSeparately(t *testing.T) {
	res := Result{Opportunities: []Opportunity{
		mk("profit-small", 10, 11, 12), // +$2 profit
		mk("profit-big", 2, 8, 20),     // +$18 profit
		mk("liquid", 10, 11, 9),        // no profit, 90% liquidity
		mk("illiquid", 10, 11, 1),      // no profit, 10% liquidity
		mk("spread-big", 1, 9, 0),      // +800% spread, the widest, no buylist
		mk("flat", 5, 5, 0),            // nothing to say
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
	if got := byKind[KindLiquid]; len(got) != 2 || got[0] != "liquid" {
		t.Errorf("liquid = %v, want the most liquid first", got)
	}
	// Spread covers everything where vendors disagree, profitable or not.
	if got := byKind[KindSpread]; len(got) != 5 || got[0] != "spread-big" {
		t.Errorf("spread = %v, want the widest first and the flat card excluded", got)
	}
}

func TestSectionsRespectTheLimit(t *testing.T) {
	var res Result
	for i := range 20 {
		res.Opportunities = append(res.Opportunities, mk("c"+strconv.Itoa(i), 1, 2, 5))
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
		mk("profit", 2, 8, 20),
		mk("liquid", 10, 11, 9),
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
	uuidOf := func(o store.OwnedFinish) string { return o.ScryfallID }

	res := Collect(owned, quotes, uuidOf, 1.0)
	if len(res.Opportunities) != 1 || res.Opportunities[0].Card.Name != "dear" {
		t.Errorf("opportunities = %+v, want only the card above the floor", res.Opportunities)
	}
	// Both had two vendors, so both counted as compared even though one was
	// filtered out of the results.
	if res.Compared != 2 {
		t.Errorf("Compared = %d, want 2", res.Compared)
	}
}
