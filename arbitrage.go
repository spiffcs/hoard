package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdArbitrage(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("arbitrage", flag.ContinueOnError)
	minValue := fs.Float64("min", 1, "ignore cards cheaper than this")
	limit := fs.Int("limit", 10, "rows per section")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	res, err := fetchArbitrage(ctx, st, *minValue)
	if err != nil {
		return err
	}
	if len(res.Opportunities) == 0 {
		fmt.Println(env.Dim()("No vendor quotes for anything you own above that value."))
		return nil
	}

	for _, sec := range arbitrage.Sections(res, *limit) {
		if len(sec.Rows) == 0 {
			continue
		}
		fmt.Println(env.Bold()(sec.Kind.Title()) + env.Dim()("  "+sec.Kind.Note()))
		if _, err := arbitrageTable(env, sec).WriteTo(os.Stdout); err != nil {
			return err
		}
		fmt.Println()
	}

	fmt.Println(env.Dim()(fmt.Sprintf(
		"%s owned printings had two or more vendors. %s listings ignored as unsupported.\n"+
			"Vendor asking and offering prices for one day, not guaranteed sales.",
		ui.Count(res.Compared), ui.Count(res.Ignored))))
	return nil
}

// arbitrageTable lays out one section. The three share a column shape so the
// tables stack without the eye having to re-find the numbers.
func arbitrageTable(env ui.Env, sec arbitrage.Section) ui.Table {
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
	for _, o := range sec.Rows {
		finish := o.Card.Finish
		if finish == "normal" {
			finish = "-"
		}
		switch sec.Kind {
		case arbitrage.KindProfit:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.BuyAt)), ui.C(o.BuyFrom),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.C("+"+ui.Money(o.Profit())))
		case arbitrage.KindLiquid:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.BuyAt)), ui.C("retail"),
				ui.C(ui.Money(o.SellAt)), ui.C(o.SellTo),
				ui.C(ui.Percent(o.Liquidity())))
		default:
			t.Add(ui.C(o.Card.Name), ui.C(o.Printing()), ui.C(finish),
				ui.C(ui.Money(o.BuyAt)), ui.C(o.BuyFrom),
				ui.C(ui.Money(o.DearAt)), ui.C(o.DearFrom),
				ui.C("+"+ui.Percent(o.Spread())))
		}
	}
	return t
}

// fetchArbitrage gathers today's vendor quotes for everything held and ranks
// them.
//
// This is the one piece the browser cannot do for itself: it needs the MTGJSON
// id resolver, which writes learned ids back to the catalog and is shared with
// update-prices. Rather than give internal/browse a network dependency and a
// second copy of that resolver, main injects this function into it.
func fetchArbitrage(ctx context.Context, st *store.Store, minValue float64) (arbitrage.Result, error) {
	owned, err := st.OwnedByFinish()
	if err != nil {
		return arbitrage.Result{}, err
	}
	if len(owned) == 0 {
		return arbitrage.Result{}, nil
	}

	need := make([]cardRef, 0, len(owned))
	for _, o := range owned {
		if o.MTGJSONUUID == "" {
			need = append(need, cardRef{ScryfallID: o.ScryfallID, SetCode: o.SetCode})
		}
	}
	uuids, err := resolveMTGJSONIDs(ctx, st, need)
	if err != nil {
		return arbitrage.Result{}, err
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
		return arbitrage.Result{}, fmt.Errorf("mtgjson quotes: %w", err)
	}

	return arbitrage.Collect(owned, quotes,
		func(o store.OwnedFinish) string { return uuidFor(o, uuids) }, minValue), nil
}

// uuidFor prefers the id stored on the card, falling back to one resolved during
// this run.
func uuidFor(o store.OwnedFinish, resolved map[string]string) string {
	if o.MTGJSONUUID != "" {
		return o.MTGJSONUUID
	}
	return resolved[o.ScryfallID]
}
