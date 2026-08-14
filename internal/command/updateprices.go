package command

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

	fmt.Fprint(env.Out, report.Movers(env.OutEnv, res.Changes, limit,
		"since the last refresh", time.Time{}))
	return nil
}
