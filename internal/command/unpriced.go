package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

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
