package command

// `hoard update-prices`: today's prices for everything held, and what moved.

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
)

// NewCmdUpdatePrices builds `hoard update-prices`.
func NewCmdUpdatePrices(a *app) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:     "update-prices",
		GroupID: groupCollection,
		Short:   "Refresh prices (Scryfall updates daily)",
		Example: "hoard update-prices [--limit N]",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cat := openCatalog()
			if cat != nil {
				defer cat.Close()
			}
			deps := action.Deps{
				Store: a.store, Catalog: cat, CacheDir: pricing.DefaultCacheDir(),
				Confirm: confirm, Resolver: cardResolver,
			}
			return runUpdatePrices(c.Context(), deps, a.env, limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", report.DefaultMoverRows, "risers/sinkers to list")
	return cmd
}

// runUpdatePrices is the render half of the command, split from the flag and
// dependency glue so a test can drive it against a fixture and read what it
// wrote. Everything goes to the Env's streams: the movers table used to sniff
// os.Stdout for its width even when the output was a test buffer.
func runUpdatePrices(ctx context.Context, deps action.Deps, env *cli.Env, limit int) error {
	pr := stderrPrinter()
	res, err := action.UpdatePrices(ctx, deps, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	r := env.Report()
	if res.Total == 0 {
		r.Result("No cards yet; nothing to update.")
		return nil
	}
	r.Result("Updated prices for %d of %d cards.", res.Found, res.Total)
	if res.FromCatalog > 0 {
		r.Detail("%d from the local catalog, %d from Scryfall.",
			res.FromCatalog, res.Found-res.FromCatalog)
	}
	if res.NotFound > 0 {
		r.Detail("%d cards could not be re-fetched from Scryfall.", res.NotFound)
	}
	fmt.Fprintln(env.Out)
	// No cutoff to name: a refresh compares against whatever the last one
	// recorded, whenever that was, so no row can start later than the window
	// and the FROM column stays out of this table.
	fmt.Fprint(env.Out, report.Movers(env.OutEnv, res.Changes, limit,
		"since the last refresh", time.Time{}))
	return nil
}
