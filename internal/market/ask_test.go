package market

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

func grief() store.OwnedFinish {
	return store.OwnedFinish{Name: "Grief", SetCode: "sld", CollectorNumber: "7006",
		Finish: finish.Foil, Copies: 1, Value: 610.49}
}

// The figures are the live ones for tcgplayer product 638143 on 2026-08-23.
func griefQuotes(lowAsk float64) []mtgjson.Quote {
	qs := []mtgjson.Quote{
		q(MarketProvider, mtgjson.Retail, finish.Foil, 610.49),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 785.00),
	}
	if lowAsk > 0 {
		qs = append(qs, q(MarketProvider, mtgjson.Ask, finish.Foil, lowAsk))
	}
	return qs
}

func TestArbitrageRequiresBeatingARealAsk(t *testing.T) {
	op, _ := Assess(grief(), griefQuotes(882.86))

	if !op.HasLowAsk || op.LowAsk != 882.86 {
		t.Fatalf("LowAsk = %v (has=%v), want the cheapest listing", op.LowAsk, op.HasLowAsk)
	}
	if op.Market != 610.49 {
		t.Errorf("Market = %v, want the tcgplayer market price kept for display", op.Market)
	}
	if op.BuyAt != 882.86 {
		t.Errorf("BuyAt = %v, want the real ask — a market price is not buyable", op.BuyAt)
	}
	if p := op.Profit(); p >= 0 {
		t.Errorf("Profit = %v, want a loss: buying at 882.86 to sell at 785.00", p)
	}

	rows := top([]Opportunity{op}, 10, KindProfit)
	if len(rows) != 0 {
		t.Errorf("ARBITRAGE listed %d rows, want none — this trade loses money", len(rows))
	}
}

func TestArbitrageStillFindsRealOpportunities(t *testing.T) {
	op, _ := Assess(grief(), griefQuotes(700.00))

	if op.BuyAt != 700 {
		t.Fatalf("BuyAt = %v, want the 700 ask", op.BuyAt)
	}
	if got, want := op.Profit(), 85.00; got != want {
		t.Errorf("Profit = %v, want %v (785 buylist less a 700 ask)", got, want)
	}
	if rows := top([]Opportunity{op}, 10, KindProfit); len(rows) != 1 {
		t.Errorf("ARBITRAGE listed %d rows, want the genuine one", len(rows))
	}
}

func TestAMarketPriceAloneIsNotAnAsk(t *testing.T) {
	op, _ := Assess(grief(), griefQuotes(0))

	if op.HasRetail {
		t.Error("HasRetail is set from a market price alone; nothing here is buyable")
	}
	if op.HasLowAsk {
		t.Error("HasLowAsk is set with no ask quote present")
	}
	if rows := top([]Opportunity{op}, 10, KindProfit); len(rows) != 0 {
		t.Errorf("ARBITRAGE listed %d rows with no verified ask, want none", len(rows))
	}
}

func TestOtherVendorsRetailPricesStillCountAsAsks(t *testing.T) {
	qs := []mtgjson.Quote{
		q(MarketProvider, mtgjson.Retail, finish.Foil, 610.49),
		q("manapool", mtgjson.Retail, finish.Foil, 720.00),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 785.00),
	}
	op, _ := Assess(grief(), qs)

	if !op.HasRetail || op.BuyAt != 720 || op.BuyFrom != "manapool" {
		t.Fatalf("BuyAt = %v from %q (has=%v), want manapool's real 720 ask",
			op.BuyAt, op.BuyFrom, op.HasRetail)
	}
	if rows := top([]Opportunity{op}, 10, KindProfit); len(rows) != 1 {
		t.Errorf("ARBITRAGE listed %d rows, want the manapool trade", len(rows))
	}
}

func TestBelowMarketComparesRealAsksToTheMarketPrice(t *testing.T) {
	op, _ := Assess(grief(), griefQuotes(400.00))

	if got := op.BelowMarket(); got < 0.34 || got > 0.35 {
		t.Errorf("BelowMarket = %v, want ~0.345 (a 400 ask against a 610.49 market)", got)
	}
	if rows := top([]Opportunity{op}, 10, KindBelowMarket); len(rows) != 1 {
		t.Errorf("BELOW MARKET listed %d rows, want the underpriced listing", len(rows))
	}
}

func TestCollectCountsRowsHiddenForWantOfAnAsk(t *testing.T) {
	owned := []store.OwnedFinish{grief()}

	res := Collect(owned, map[string][]mtgjson.Quote{"": griefQuotes(0)}, 0)
	if res.Unverified != 1 {
		t.Errorf("Unverified = %d, want 1 — a buylist with no verified ask was dropped",
			res.Unverified)
	}

	res = Collect(owned, map[string][]mtgjson.Quote{"": griefQuotes(700)}, 0)
	if res.Unverified != 0 {
		t.Errorf("Unverified = %d, want 0 when the ask is known", res.Unverified)
	}
}

func wanted(o store.OwnedFinish) store.OwnedFinish { o.Copies, o.Value = 0, 0; return o }

func TestSellSideSkipsCardsYouDoNotOwn(t *testing.T) {
	qs := []mtgjson.Quote{
		q(MarketProvider, mtgjson.Retail, finish.Foil, 100),
		q(MarketProvider, mtgjson.Ask, finish.Foil, 50),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 80),
	}
	op, _ := Assess(wanted(grief()), qs)

	if op.Profit() <= 0 {
		t.Fatalf("setup: profit = %v, want a genuine spread to test against", op.Profit())
	}
	if rows := top([]Opportunity{op}, 10, KindProfit); len(rows) != 0 {
		t.Errorf("ARBITRAGE listed %d rows for a card held 0 times; you cannot sell it", len(rows))
	}
	if rows := top([]Opportunity{op}, 10, KindLiquid); len(rows) != 0 {
		t.Errorf("BUYLIST NEAR MARKET listed %d rows for a card you do not own", len(rows))
	}
	if rows := top([]Opportunity{op}, 10, KindBelowMarket); len(rows) != 1 {
		t.Errorf("BELOW MARKET listed %d rows, want the want kept — it is a buy signal", len(rows))
	}
}

func TestSellSideStillWorksForCardsYouHold(t *testing.T) {
	qs := []mtgjson.Quote{
		q(MarketProvider, mtgjson.Retail, finish.Foil, 100),
		q(MarketProvider, mtgjson.Ask, finish.Foil, 50),
		q("cardkingdom", mtgjson.Buylist, finish.Foil, 80),
	}
	op, _ := Assess(grief(), qs)

	if rows := top([]Opportunity{op}, 10, KindProfit); len(rows) != 1 {
		t.Errorf("ARBITRAGE listed %d rows for a card you hold, want 1", len(rows))
	}
}
