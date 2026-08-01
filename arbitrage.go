package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/spiffcs/hoard/internal/action"
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
	pr := stderrPrinter()
	res, err := action.Arbitrage(ctx, action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()},
		pr.Fn(), *minValue)
	pr.Close()
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
