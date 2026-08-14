package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

func NewCmdRefused(a *app) *cobra.Command {
	return cli.JSONCapable(&cobra.Command{
		Use:     "refused",
		GroupID: groupCollection,
		Short:   "Prices replaced by the cheapest ask, and what they were",
		Long: `Prices hoard declined, and what it used instead.

A market price averages completed sales. Where a card
has not sold — a preorder, a printing nobody has
traded — that average is taken over nothing, and what
comes back is not a low price but a non-price.

When one sits far below the cheapest ask on the card's
own TCGplayer listing, hoard reports the ask instead
and records the swap here.

Corrections are re-derived on every price run, and
lapse on their own once the feed's figure is sane.`,
		Example: "hoard refused\nhoard refused --json",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runRefused(a.store, a.env)
		},
	})
}

func runRefused(st *store.Store, env *cli.Env) error {
	rows, err := st.PriceOverrides()
	if err != nil {
		return err
	}
	if env.JSON {
		return hoardjson.Write(env.Out, hoardjson.FromRefused(rows))
	}
	fmt.Fprint(env.Out, report.Refused(env.OutEnv, rows))
	return nil
}
