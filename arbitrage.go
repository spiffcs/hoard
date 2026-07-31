package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdArbitrage(ctx context.Context, st *store.Store, args []string, jsonOut bool) error {
	fs := flag.NewFlagSet("arbitrage", flag.ContinueOnError)
	minValue := fs.Float64("min", 1, "ignore cards cheaper than this")
	limit := fs.Int("limit", 10, "rows per section")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	res, err := fetchArbitrage(ctx, newFetcher(st), st, *minValue)
	if err != nil {
		return err
	}
	if jsonOut {
		// --min still shapes the data (it is a selection, not a truncation);
		// --limit does not, for the same reason movers emits everything.
		return hoardjson.Write(os.Stdout, hoardjson.FromArbitrage(res))
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
		fmt.Print(report.Arbitrage(env, sec))
		fmt.Println()
	}

	fmt.Println(env.Dim()(fmt.Sprintf(
		"%s owned printings had two or more vendors. %s listings ignored as unsupported.\n"+
			"Vendor asking and offering prices for one day, not guaranteed sales.",
		ui.Count(res.Compared), ui.Count(res.Ignored))))
	return nil
}

// fetchArbitrage gathers today's vendor quotes for everything held and ranks
// them. The fetcher is a parameter because the two callers need different
// progress wiring: the CLI reports on stderr, the browse TUI must stay silent
// or the lines land inside its alt-screen frame.
//
// The browser cannot do this itself without taking on a network dependency, so
// main injects it (see browse.WithArbitrage).
func fetchArbitrage(ctx context.Context, f *pricing.Fetcher, st *store.Store, minValue float64) (arbitrage.Result, error) {
	owned, err := st.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return arbitrage.Result{}, err
	}
	refs := make([]pricing.Ref, len(owned))
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
	}
	quotes, err := f.Quotes(ctx, refs)
	if err != nil {
		return arbitrage.Result{}, err
	}
	return arbitrage.Collect(owned, quotes, minValue), nil
}
