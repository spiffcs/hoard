package command

// `hoard unpriced`: the holdings counting as $0.00, and the reason each one is.

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

// NewCmdUnpriced builds `hoard unpriced`.
func NewCmdUnpriced(a *app) *cobra.Command {
	return cli.JSONCapable(&cobra.Command{
		Use:     "unpriced",
		GroupID: groupCollection,
		Short:   "Cards counting as $0.00, and why",
		Example: "hoard unpriced",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runUnpriced(a.store, a.env)
		},
	})
}

// runUnpriced lists what is contributing nothing to your totals.
//
// A card with no price is valued at zero, which is indistinguishable on a table
// from a card genuinely worth nothing. This is how you tell the difference, and
// how you find out which deck's total is understated.
func runUnpriced(st *store.Store, env *cli.Env) error {
	rows, err := action.Deps{Store: st}.Unpriced()
	if err != nil {
		return err
	}
	if env.JSON {
		return hoardjson.Write(env.Out, hoardjson.FromUnpriced(rows))
	}
	fmt.Fprint(env.Out, report.Unpriced(env.OutEnv, rows))
	if len(rows) > 0 {
		fmt.Fprint(env.Out, report.UnpricedAdvice(env.OutEnv))
	}
	return nil
}
