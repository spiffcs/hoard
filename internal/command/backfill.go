package command

// `hoard backfill-prices`: the price history MTGJSON kept while hoard was not
// looking.

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// NewCmdBackfillPrices builds `hoard backfill-prices`.
func NewCmdBackfillPrices(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "backfill-prices",
		GroupID: groupCollection,
		Short:   "Load 90 days of past prices from MTGJSON",
		Example: "hoard backfill-prices",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runBackfillPrices(c.Context(), a.store, a.env)
		},
	}
}

// runBackfillPrices loads the prices MTGJSON kept while hoard was not
// looking, so a fresh hoard can answer "what moved this month" immediately.
// A one-off, and separate from update-prices for a reason: the archive is
// ~150 MB against the 5 MB of today's file, and the download cache is
// pruned nightly.
func runBackfillPrices(ctx context.Context, st *store.Store, env *cli.Env) error {
	pr := stderrPrinter()
	res, err := action.BackfillPrices(ctx,
		action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()}, pr.Fn(), 90)
	pr.Close()
	if err != nil {
		return err
	}
	if res.Printings == 0 {
		fmt.Fprintln(env.Out, env.OutEnv.Dim()("Nothing owned yet."))
		return nil
	}
	printBackfill(env, res)
	return nil
}

// printBackfill reports the import, including what it missed. The outcome
// goes to stdout; the partial-outcome warnings (printings the archive
// could not cover) go to stderr — this used to put everything on stdout,
// so piping the result meant filtering the caveats out by hand.
func printBackfill(env *cli.Env, r action.BackfillResult) {
	rep := env.Report()
	if r.AlreadyToday != "" {
		rep.Hint("Already backfilled today for these holdings; the archive only changes daily.")
		return
	}
	if r.Inserted == 0 {
		if r.HadHistorySince != "" {
			if t, err := time.Parse(time.RFC3339, r.HadHistorySince); err == nil {
				rep.Hint("Nothing to backfill: prices are already recorded from %s.",
					t.Local().Format("2 Jan 2006"))
				return
			}
		}
		rep.Hint("MTGJSON had no earlier prices for anything you hold.")
		return
	}

	rep.Result("Backfilled %s observations across %s printings.",
		ui.Count(r.Inserted), ui.Count(r.Cards))
	if r.Unmapped > 0 {
		rep.Warn("%s printings have no MTGJSON id and were skipped.", ui.Count(r.Unmapped))
	}
	if r.Unquoted > 0 {
		rep.Warn("%s have no TCGplayer price history, the same gap 'unpriced' reports.",
			ui.Count(r.Unquoted))
	}
	rep.Hint("Prices come from TCGplayer, the source Scryfall itself quotes.")
	rep.Result("")
	rep.Hint("Try: hoard movers --since 30d")
}
