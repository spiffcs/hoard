package command

import (
	"context"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func NewCmdRepairFinishes(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "repair-finishes",
		GroupID: groupCollection,
		Short:   "Fix cards stored as a finish they lack",
		Example: "hoard repair-finishes",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runRepairFinishes(c.Context(), a.store, a.env)
		},
	}
}

func runRepairFinishes(ctx context.Context, st *store.Store, env *cli.Env) error {
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	pr := stderrPrinter()
	res, err := action.RepairFinishes(ctx,
		action.Deps{Store: st, Catalog: cat, Resolver: cardResolver}, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	if res.Total == 0 {
		fmt.Fprintln(env.Out, "No cards yet; nothing to repair.")
		return nil
	}
	fixed, ambiguous := res.Fixed, res.Ambiguous

	dim := env.OutEnv.Dim()
	if len(fixed) == 0 && len(ambiguous) == 0 {
		fmt.Fprintln(env.Out, dim("Every card is recorded in a finish it actually comes in."))
		return nil
	}

	if len(fixed) > 0 {
		fmt.Fprint(env.Out, report.FinishRepairs(env.OutEnv, fixed))
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"\nCorrected %s entries. Run hoard update-prices to value them.", ui.Count(len(fixed)))))
	}
	for _, a := range ambiguous {
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"  left alone: %s (%s/%s) is recorded as %s but comes in %s",
			a.Name, a.SetCode, a.CollectorNumber, a.From, strings.Join(finishNames(a.Available), "|"))))
	}
	return nil
}

func finishNames(fs []finish.Finish) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.String()
	}
	return out
}
