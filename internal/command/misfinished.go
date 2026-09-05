package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func NewCmdMisfinished(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "misfinished",
		GroupID: groupCollection,
		Short:   "Cards stored as a finish their printing lacks",
		Example: "hoard misfinished",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runMisfinished(c.Context(), a.store, a.env)
		},
	}
}

func runMisfinished(ctx context.Context, st *store.Store, env *cli.Env) error {
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	pr := stderrPrinter()
	res, err := action.Misfinished(ctx,
		action.Deps{Store: st, Catalog: cat, Resolver: cardResolver}, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	if res.Total == 0 {
		fmt.Fprintln(env.Out, "No cards yet; nothing to check.")
		return nil
	}
	fixable, ambiguous := res.Fixable, res.Ambiguous

	dim := env.OutEnv.Dim()
	if len(fixable) == 0 && len(ambiguous) == 0 {
		fmt.Fprintln(env.Out, dim("Every card is recorded in a finish it actually comes in."))
		return nil
	}

	if len(fixable) > 0 {
		fmt.Fprint(env.Out, report.Misfinished(env.OutEnv, fixable))
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"\n%s recorded in a finish the printing does not come in. "+
				"Change the finish from the card detail in hoard browse.",
			ui.Plural(len(fixable), "entry is", "entries are"))))
	}
	for _, a := range ambiguous {
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"  %s (%s/%s) is recorded as %s but comes in %s",
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
