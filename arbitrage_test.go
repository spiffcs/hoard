package main

import (
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
	op, usable, dropped := assess(ownedFoil("Legion Loyalty"), qs)

	if dropped != 1 {
		t.Errorf("dropped = %d, want the manapool listing discarded", dropped)
	}
	if usable != 1 {
		t.Errorf("usable retail = %d, want 1", usable)
	}
	if op.buyAt != 2.49 || op.buyFrom != "cardkingdom" {
		t.Errorf("buy = %v from %q, want 2.49 cardkingdom", op.buyAt, op.buyFrom)
	}
	// With the bogus quote gone the only survivor is also the cheapest, so there
	// is no spread left to report.
	if op.dearAt != 2.49 {
		t.Errorf("dear = %v, want the surviving quote", op.dearAt)
	}
	if op.spread() != 0 {
		t.Errorf("spread = %v, want none once the outlier is gone", op.spread())
	}
}

// The widest genuine disagreement measured was 9.3x. It must survive.
func TestAssessKeepsWideButRealSpread(t *testing.T) {
	qs := []mtgjson.Quote{
		q("cardkingdom", mtgjson.Retail, "foil", 4.49),
		q("manapool", mtgjson.Retail, "foil", 41.68),
	}
	op, _, dropped := assess(ownedFoil("Siege-Gang Lieutenant"), qs)
	if dropped != 0 {
		t.Errorf("dropped %d, want a 9.3x spread kept", dropped)
	}
	if op.dearAt != 41.68 || op.dearFrom != "manapool" {
		t.Errorf("dear = %v from %q, want 41.68 manapool", op.dearAt, op.dearFrom)
	}
	if got := op.spread(); got < 8.2 || got > 8.3 {
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
	op, usable, _ := assess(ownedFoil("Legion Loyalty"), qs)
	if usable != 1 || op.buyAt != 2.49 {
		t.Errorf("buy = %v (%d usable), want only the foil retail 2.49", op.buyAt, usable)
	}
	if op.sellAt != 0.75 {
		t.Errorf("sell = %v, want the foil buylist 0.75", op.sellAt)
	}

	// The same card owned in non-foil reads the other quotes instead.
	normal := ownedFoil("Legion Loyalty")
	normal.Finish = "normal"
	op, usable, _ = assess(normal, qs)
	if usable != 2 || op.buyAt != 0.42 || op.sellAt != 0.10 {
		t.Errorf("non-foil: buy %v sell %v (%d usable), want 0.42 / 0.10",
			op.buyAt, op.sellAt, usable)
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
	op, _, _ := assess(o, qs)
	if !op.hasBuy || op.profit() < 2.06 || op.profit() > 2.08 {
		t.Errorf("profit = %v, want about 2.07", op.profit())
	}
	if op.liquidity() <= 1 {
		t.Errorf("liquidity = %v, want above 1 when a shop pays over retail", op.liquidity())
	}
}

// A card nobody quotes must not reach the report at all.
func TestAssessWithNoRetailIsSkipped(t *testing.T) {
	op, usable, dropped := assess(ownedFoil("Unquoted"), []mtgjson.Quote{
		q("cardkingdom", mtgjson.Buylist, "foil", 0.75),
	})
	if op.hasRetail || usable != 0 || dropped != 0 {
		t.Errorf("op = %+v (%d usable, %d dropped), want nothing usable", op, usable, dropped)
	}
}

func TestTopFiltersSortsAndTruncates(t *testing.T) {
	mk := func(name string, buy, dear float64) opportunity {
		return opportunity{card: store.OwnedFinish{Name: name},
			buyAt: buy, dearAt: dear, hasRetail: true}
	}
	all := []opportunity{
		mk("small", 10, 11), // +10%
		mk("flat", 5, 5),    // no spread, filtered out
		mk("big", 2, 8),     // +300%
		mk("mid", 4, 8),     // +100%
	}
	got := top(all, 2,
		func(o opportunity) bool { return o.dearAt > o.buyAt },
		func(a, b opportunity) int {
			if a.spread() == b.spread() {
				return 0
			}
			if a.spread() > b.spread() {
				return -1
			}
			return 1
		})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want the limit of 2", len(got))
	}
	if got[0].card.Name != "big" || got[1].card.Name != "mid" {
		t.Errorf("order = %s, %s; want big then mid", got[0].card.Name, got[1].card.Name)
	}
}
