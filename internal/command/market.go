package command

// `hoard market`: what vendors are asking and bidding against TCGplayer's
// last-sold prices.

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// NewCmdMarket builds `hoard market`.
func NewCmdMarket(a *app) *cobra.Command {
	var minValue float64
	var limit int

	cmd := &cobra.Command{
		Use:     "market",
		GroupID: groupCollection,
		Short:   "Vendor prices vs TCGplayer's last-sold prices",
		Example: "hoard market [--min N] [--limit N]",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runMarket(c.Context(), a.store, a.env, minValue, limit)
		},
	}
	cmd.Flags().Float64Var(&minValue, "min", 1, "ignore cards cheaper than this")
	cmd.Flags().IntVar(&limit, "limit", 10, "rows per section")
	return cli.JSONCapable(cmd)
}

func runMarket(ctx context.Context, st *store.Store, env *cli.Env, minValue float64, limit int) error {
	pr := stderrPrinter()
	res, err := action.Market(ctx, action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()},
		pr.Fn(), minValue)
	pr.Close()
	if err != nil {
		return err
	}
	if env.JSON {
		// --min still shapes the data (it is a selection, not a truncation);
		// --limit does not, for the same reason movers emits everything.
		return hoardjson.Write(env.Out, hoardjson.FromMarket(res))
	}

	out, e := env.Out, env.OutEnv
	if len(res.Opportunities) == 0 {
		fmt.Fprintln(out, e.Dim()("No vendor quotes for anything you own above that value."))
		return nil
	}

	for _, sec := range market.Sections(res, limit) {
		if len(sec.Rows) == 0 {
			continue
		}
		fmt.Fprint(out, report.Market(e, sec))
		fmt.Fprintln(out)
	}
	if comps := market.TopComps(res.Comps, limit); len(comps) > 0 {
		fmt.Fprint(out, report.Comps(e, comps))
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, e.Dim()(fmt.Sprintf(
		"%s owned printings had two or more vendors. Anchored on tcgplayer's\n"+
			"sales-derived market price; asks and offers are one day's, not guaranteed.\n"+
			"Spread is retail minus buylist over retail: tight means the price is real.",
		ui.Count(res.Compared))))
	return nil
}
