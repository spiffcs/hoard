package command

// The report command: a dated valuation of the whole hoard — totals, each
// binder, the most valuable holdings, and where every price came from — in a
// form worth handing to an insurer or a buyer.

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// NewCmdReport builds `hoard report`.
func NewCmdReport(a *app) *cobra.Command {
	var top int
	var csvOut bool
	var outPath string

	cmd := &cobra.Command{
		Use:     "report",
		GroupID: groupCollection,
		Short:   "Dated valuation: totals, binders, top holdings",
		Example: "hoard report [--top N] [--csv] [-o FILE]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runReport(a.store, a.env, top, csvOut, outPath)
		},
	}
	cmd.Flags().IntVar(&top, "top", 10, "holdings to itemize in the text report")
	cmd.Flags().BoolVar(&csvOut, "csv", false, "write the full itemized valuation as CSV")
	// -o is declared as the shorthand of --output rather than as a flag named
	// "o": pflag reads a single dash as a run of shorthands, so a shorthand has
	// to hang off a long name. -o keeps working exactly as before; --output is
	// the spelling that had to come with it.
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cli.JSONCapable(cmd)
}

func runReport(st *store.Store, env *cli.Env, top int, csvOut bool, outPath string) error {
	if csvOut && env.JSON {
		return cli.Usagef("choose --csv or --json, not both")
	}

	d, err := action.Deps{Store: st}.Valuation(top)
	if err != nil {
		return err
	}

	// Detect on the actual destination: a report written to a file must be
	// plain text however colorful the terminal running the command.
	emit := func(w io.Writer, e ui.Env) error {
		switch {
		case env.JSON:
			return hoardjson.Write(w, hoardjson.FromValuation(d))
		case csvOut:
			owned, err := st.OwnedByFinish()
			if err != nil {
				return err
			}
			return report.ValuationCSV(w, d.AsOf, owned)
		default:
			_, err := io.WriteString(w, report.Valuation(e, d))
			return err
		}
	}
	if outPath == "" {
		return emit(env.Out, env.OutEnv)
	}
	f, err := createOutput(outPath)
	if err != nil {
		return err
	}
	if err := emit(f, ui.Detect(f.f)); err != nil {
		f.Abort()
		return err
	}
	return f.Commit()
}
