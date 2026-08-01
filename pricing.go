package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdUpdatePrices(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("update-prices", flag.ContinueOnError)
	limit := fs.Int("limit", report.DefaultMoverRows, "risers/sinkers to list")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	deps := action.Deps{
		Store: st, Catalog: cat, CacheDir: pricing.DefaultCacheDir(),
		Confirm: confirm, Resolver: cardResolver,
	}
	return runUpdatePrices(ctx, deps, *limit, os.Stdout)
}

// runUpdatePrices is the render half of the command, split from the flag and
// dependency glue so a test can drive it against a fixture and lock stdout.
func runUpdatePrices(ctx context.Context, deps action.Deps, limit int, w io.Writer) error {
	pr := stderrPrinter()
	res, err := action.UpdatePrices(ctx, deps, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	if res.Total == 0 {
		fmt.Fprintln(w, "No cards yet; nothing to update.")
		return nil
	}
	fmt.Fprintf(w, "Updated prices for %d of %d cards.\n", res.Found, res.Total)
	if res.FromCatalog > 0 {
		fmt.Fprintf(w, "  %d from the local catalog, %d from Scryfall.\n",
			res.FromCatalog, res.Found-res.FromCatalog)
	}
	if res.NotFound > 0 {
		fmt.Fprintf(w, "  %d cards could not be re-fetched from Scryfall.\n", res.NotFound)
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, report.Movers(ui.Detect(os.Stdout), res.Changes, limit, "since the last refresh"))
	return nil
}

// cmdUnpriced lists what is contributing nothing to your totals.
//
// A card with no price is valued at zero, which is indistinguishable on a table
// from a card genuinely worth nothing. This is how you tell the difference, and
// how you find out which deck's total is understated.
func cmdUnpriced(st *store.Store, jsonOut bool) error {
	rows, err := st.Unpriced()
	if err != nil {
		return err
	}
	if jsonOut {
		return hoardjson.Write(os.Stdout, hoardjson.FromUnpriced(rows))
	}
	env := ui.Detect(os.Stdout)
	fmt.Print(report.Unpriced(env, rows))
	if len(rows) > 0 {
		fmt.Print(report.UnpricedAdvice(env))
	}
	return nil
}
