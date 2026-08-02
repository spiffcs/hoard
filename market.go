package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdMarket(ctx context.Context, st *store.Store, args []string, jsonOut bool) error {
	fs := flag.NewFlagSet("market", flag.ContinueOnError)
	minValue := fs.Float64("min", 1, "ignore cards cheaper than this")
	limit := fs.Int("limit", 10, "rows per section")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	pr := stderrPrinter()
	res, err := action.Market(ctx, action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()},
		pr.Fn(), *minValue)
	pr.Close()
	if err != nil {
		return err
	}
	if jsonOut {
		// --min still shapes the data (it is a selection, not a truncation);
		// --limit does not, for the same reason movers emits everything.
		return hoardjson.Write(os.Stdout, hoardjson.FromMarket(res))
	}
	if len(res.Opportunities) == 0 {
		fmt.Println(env.Dim()("No vendor quotes for anything you own above that value."))
		return nil
	}

	for _, sec := range market.Sections(res, *limit) {
		if len(sec.Rows) == 0 {
			continue
		}
		fmt.Print(report.Market(env, sec))
		fmt.Println()
	}

	fmt.Println(env.Dim()(fmt.Sprintf(
		"%s owned printings had two or more vendors. Anchored on tcgplayer's\n"+
			"sales-derived market price; asks and offers are one day's, not guaranteed.",
		ui.Count(res.Compared))))
	return nil
}
