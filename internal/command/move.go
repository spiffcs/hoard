package command

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/ui"
)

type moveOpts struct {
	to     string
	dryRun bool
	yes    bool
}

func NewCmdMove(a *app) *cobra.Command {
	var o moveOpts

	cmd := &cobra.Command{
		Use:     "move --to BINDER",
		GroupID: groupBinder,
		Short:   "Move piped holdings into a binder",
		Long: "Move holdings into a binder, reading the document to\n" +
			"move on stdin. Pair it with an export, which chooses\n" +
			"what moves:\n\n" +
			"  hoard export --binder Binder --json --filter 'price<1' |\n" +
			"    hoard move --to bulk\n\n" +
			"Only binder cards move. A document that names deck\n" +
			"cards — which is what --all gives you — has those rows\n" +
			"skipped and counted, so a decklist is never touched.\n\n" +
			"This moves cards between binders. To file a whole deck\n" +
			"under a folder, use deck move.",
		Example: "hoard export --binder B --json |\n" +
			"  hoard move --to BINDER [--dry-run] [--yes]",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runMove(a, c, o)
		},
	}
	cmd.Flags().StringVar(&o.to, "to", "", "binder the holdings move into")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "report what would move, but write nothing")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "move without asking first")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func moveConfirmer(a *app, yes bool) func(string) bool {
	if yes {
		return func(string) bool { return true }
	}
	if a.confirmMove != nil {
		return a.confirmMove
	}
	return confirmOnTerminal
}

func runMove(a *app, c *cobra.Command, o moveOpts) error {
	doc, err := io.ReadAll(c.InOrStdin())
	if err != nil {
		return fmt.Errorf("reading the holdings document: %w", err)
	}

	res, err := action.MoveHoldings(
		action.Deps{Store: a.store, Confirm: moveConfirmer(a, o.yes)},
		action.MoveOptions{Document: doc, To: o.to, DryRun: o.dryRun})
	switch {
	case errors.Is(err, action.ErrNotConfirmed):
		return fmt.Errorf("%w · re-run with --yes to move without being asked", err)
	case err != nil:
		return err
	}

	r := a.env.Report()
	r.Result("%s %s of %s into %q · %s",
		dryRunVerb(o.dryRun, "Moved", "Would move"),
		ui.Plural(res.Copies, "copy", "copies"),
		ui.Plural(res.Printings, "printing", "printings"), res.Target,
		ui.Money(res.Value))
	if res.AlreadyThere > 0 {
		r.Detail("%s already there.", ui.Plural(res.AlreadyThere, "copy", "copies"))
	}
	if res.SkippedRows > 0 {
		r.Warn("skipped %s in %s — move only touches binders",
			ui.Plural(res.SkippedRows, "row", "rows"),
			ui.Plural(len(res.SkippedDecks), "deck", "decks"))
		for _, d := range res.SkippedDecks {
			r.Item(d)
		}
	}
	return nil
}
