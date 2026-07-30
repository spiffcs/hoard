package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// outlierFactor is how far above the cheapest quote a price may sit before it is
// treated as unsupported rather than as an opportunity.
//
// Chosen from the real spread distribution: the widest genuine disagreement
// measured was 9.3x (Siege-Gang Lieutenant, $4.49 against $41.68), while
// Manapool lists one card at 55,600x what two other vendors charge. Twenty
// leaves room on both sides of that gap.
const outlierFactor = 20

// opportunity is one owned printing-and-finish, with the vendor quotes that make
// it interesting.
type opportunity struct {
	card store.OwnedFinish

	buyAt     float64 // cheapest retail
	buyFrom   string
	dearAt    float64 // dearest retail that another vendor corroborates
	dearFrom  string
	sellAt    float64 // best buylist offer
	sellTo    string
	hasRetail bool
	hasBuy    bool
}

// spread is how much dearer the priciest vendor is than the cheapest.
func (o opportunity) spread() float64 { return o.dearAt/o.buyAt - 1 }

// liquidity is the fraction of the cheapest retail price a shop will pay.
func (o opportunity) liquidity() float64 { return o.sellAt / o.buyAt }

// profit is what is left after buying at the cheapest retail and selling to the
// best buylist. Positive is genuine arbitrage.
func (o opportunity) profit() float64 { return o.sellAt - o.buyAt }

func cmdArbitrage(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("arbitrage", flag.ContinueOnError)
	minValue := fs.Float64("min", 1, "ignore cards cheaper than this")
	limit := fs.Int("limit", 10, "rows per section")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	owned, err := st.OwnedByFinish()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	if len(owned) == 0 {
		fmt.Println(env.Dim()("Nothing owned yet."))
		return nil
	}

	need := make([]cardRef, 0, len(owned))
	for _, o := range owned {
		if o.MTGJSONUUID == "" {
			need = append(need, cardRef{ScryfallID: o.ScryfallID, SetCode: o.SetCode})
		}
	}
	uuids, err := resolveMTGJSONIDs(ctx, st, need)
	if err != nil {
		return err
	}

	want := make(map[string]bool, len(owned))
	for _, o := range owned {
		if u := uuidFor(o, uuids); u != "" {
			want[u] = true
		}
	}
	mtgjson.CacheDir = priceCacheDir()
	quotes, err := mtgjson.TodayQuotes(ctx, want)
	if err != nil {
		return fmt.Errorf("mtgjson quotes: %w", err)
	}

	var opps []opportunity
	var compared, ignored int
	for _, o := range owned {
		qs := quotes[uuidFor(o, uuids)]
		if len(qs) == 0 {
			continue
		}
		op, retailCount, dropped := assess(o, qs)
		ignored += dropped
		if retailCount >= 2 {
			compared++
		}
		// Filter on what you would actually pay, not on what hoard thinks the
		// card is worth. A 900% spread between $0.20 and $1.99 is arithmetic,
		// not an opportunity, and sorting by percentage would otherwise fill
		// every section with cards worth less than the postage.
		if op.hasRetail && op.buyAt >= *minValue {
			opps = append(opps, op)
		}
	}

	if len(opps) == 0 {
		fmt.Println(env.Dim()("No vendor quotes for anything you own above that value."))
		return nil
	}

	sections := []struct {
		title string
		note  string
		rows  []opportunity
	}{
		{"ARBITRAGE", "a shop pays more than the cheapest retail",
			top(opps, *limit, func(o opportunity) bool { return o.hasBuy && o.profit() > 0 },
				func(a, b opportunity) int { return cmp.Compare(b.profit(), a.profit()) })},
		{"EASY TO SELL", "buylist is close to retail",
			top(opps, *limit, func(o opportunity) bool { return o.hasBuy && o.profit() <= 0 },
				func(a, b opportunity) int { return cmp.Compare(b.liquidity(), a.liquidity()) })},
		{"CHEAPEST VS DEAREST", "where the vendors disagree",
			top(opps, *limit, func(o opportunity) bool { return o.dearAt > o.buyAt },
				func(a, b opportunity) int { return cmp.Compare(b.spread(), a.spread()) })},
	}

	for _, sec := range sections {
		if len(sec.rows) == 0 {
			continue
		}
		fmt.Println(env.Bold()(sec.title) + env.Dim()("  "+sec.note))
		t := ui.Table{
			Env: env,
			Cols: []ui.Col{
				{Align: ui.Left, Flex: true, Min: 16},
				{Align: ui.Left, Priority: 4, Style: env.Dim()},
				{Align: ui.Left, Priority: 5, Style: env.Dim()},
				{Align: ui.Right},
				{Align: ui.Left, Style: env.Dim()},
				{Align: ui.Right},
				{Align: ui.Left, Style: env.Dim()},
				{Align: ui.Right},
			},
		}
		for _, o := range sec.rows {
			finish := o.card.Finish
			if finish == "normal" {
				finish = "-"
			}
			switch sec.title {
			case "ARBITRAGE":
				t.Add(ui.C(o.card.Name), ui.C(printingOf(o.card)), ui.C(finish),
					ui.C(ui.Money(o.buyAt)), ui.C(o.buyFrom),
					ui.C(ui.Money(o.sellAt)), ui.C(o.sellTo),
					ui.C("+"+ui.Money(o.profit())))
			case "EASY TO SELL":
				t.Add(ui.C(o.card.Name), ui.C(printingOf(o.card)), ui.C(finish),
					ui.C(ui.Money(o.buyAt)), ui.C("retail"),
					ui.C(ui.Money(o.sellAt)), ui.C(o.sellTo),
					ui.C(ui.Percent(o.liquidity())))
			default:
				t.Add(ui.C(o.card.Name), ui.C(printingOf(o.card)), ui.C(finish),
					ui.C(ui.Money(o.buyAt)), ui.C(o.buyFrom),
					ui.C(ui.Money(o.dearAt)), ui.C(o.dearFrom),
					ui.C("+"+ui.Percent(o.spread())))
			}
		}
		if _, err := t.WriteTo(os.Stdout); err != nil {
			return err
		}
		fmt.Println()
	}

	fmt.Println(env.Dim()(fmt.Sprintf(
		"%s owned printings had two or more vendors. %s listings ignored as unsupported.\n"+
			"Vendor asking and offering prices for one day, not guaranteed sales.",
		ui.Count(compared), ui.Count(ignored))))
	return nil
}

// uuidFor prefers the id stored on the card, falling back to one resolved during
// this run.
func uuidFor(o store.OwnedFinish, resolved map[string]string) string {
	if o.MTGJSONUUID != "" {
		return o.MTGJSONUUID
	}
	return resolved[o.ScryfallID]
}

func printingOf(o store.OwnedFinish) string { return o.SetCode + "/" + o.CollectorNumber }

// assess reduces one card's quotes to the best buy, the best corroborated sell,
// and the best buylist offer, for the finish actually owned.
//
// It reports how many retail quotes were usable and how many were discarded as
// unsupported, so the caller can say so rather than silently hiding them.
func assess(o store.OwnedFinish, qs []mtgjson.Quote) (op opportunity, retailCount, dropped int) {
	op.card = o
	finish := "normal"
	if o.Finish == "foil" || o.Finish == "etched" {
		finish = "foil"
	}

	var retail []mtgjson.Quote
	for _, q := range qs {
		if q.Finish != finish || q.Price <= 0 {
			continue
		}
		switch q.Kind {
		case mtgjson.Retail:
			retail = append(retail, q)
		case mtgjson.Buylist:
			if q.Price > op.sellAt {
				op.sellAt, op.sellTo, op.hasBuy = q.Price, q.Provider, true
			}
		}
	}
	if len(retail) == 0 {
		return op, 0, 0
	}

	slices.SortFunc(retail, func(a, b mtgjson.Quote) int { return cmp.Compare(a.Price, b.Price) })
	op.buyAt, op.buyFrom, op.hasRetail = retail[0].Price, retail[0].Provider, true

	// Walk down from the dearest until one is close enough to the cheapest to be
	// believable. A lone vendor quoting many times what everyone else charges is
	// a broken listing, not an opportunity, and it would otherwise top the list.
	for i := len(retail) - 1; i >= 0; i-- {
		if retail[i].Price <= op.buyAt*outlierFactor {
			op.dearAt, op.dearFrom = retail[i].Price, retail[i].Provider
			break
		}
		dropped++
	}
	return op, len(retail) - dropped, dropped
}

// top filters, sorts and truncates one section's rows.
func top(all []opportunity, limit int, keep func(opportunity) bool,
	order func(a, b opportunity) int) []opportunity {
	var out []opportunity
	for _, o := range all {
		if keep(o) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, order)
	return out[:min(len(out), limit)]
}
