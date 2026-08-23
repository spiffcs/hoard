package report

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func TestArbitrageShowsWhatYouBuyAtAndFromWhom(t *testing.T) {
	// Ygra, Eater of All (blb/294 foil) as the live quotes had it:
	// the profit comes from manapool's 20.82 ask, not tcgplayer's 35.64 market.
	sec := market.Section{Kind: market.KindProfit, Rows: []market.Opportunity{{
		Card: store.OwnedFinish{Name: "Ygra, Eater of All", SetCode: "blb",
			CollectorNumber: "294", Finish: finish.Foil, Copies: 1},
		Market: 35.64, BuyAt: 20.82, BuyFrom: "manapool", LowAsk: 37.12,
		SellAt: 25.00, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true, HasLowAsk: true,
	}}}

	out := Market(ui.Env{Width: 120, Clamp: true}, sec)

	if !strings.Contains(out, "$20.82") {
		t.Errorf("the acquisition price is not shown; the profit is unexplainable:\n%s", out)
	}
	if !strings.Contains(out, "manapool") {
		t.Errorf("the vendor you would buy from is not shown:\n%s", out)
	}
	if !strings.Contains(out, "$25.00") {
		t.Errorf("the buylist price is missing:\n%s", out)
	}
	if !strings.Contains(out, "+$4.18") {
		t.Errorf("the profit is missing:\n%s", out)
	}
	if !strings.Contains(out, "cardkingdom") {
		t.Errorf("the vendor you would sell to is not shown:\n%s", out)
	}
}
