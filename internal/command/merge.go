package command

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
)

type mergeOpts struct {
	dryRun         bool
	again          bool
	replaceDecks   bool
	replaceWatches bool
	outPath        string
}

func NewCmdMerge(a *app) *cobra.Command {
	var o mergeOpts

	cmd := &cobra.Command{
		Use:     "merge DATABASE",
		GroupID: groupInterop,
		Short:   "Fold another hoard database into this one",
		Example: "hoard merge OTHER.db [--dry-run] [--again]\n" +
			"       [--replace-decks] [--replace-watches] [-o FILE]",

		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Usagef("merge needs exactly one hoard database")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return runMerge(a, args[0], o)
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "report what would be merged, but write nothing")
	cmd.Flags().BoolVar(&o.again, "again", false,
		"merge a database this hoard has already merged, adding its cards a second time")
	cmd.Flags().BoolVar(&o.replaceDecks, "replace-decks", false,
		"overwrite decks this hoard already has from the same origin")
	cmd.Flags().BoolVar(&o.replaceWatches, "replace-watches", false,
		"overwrite the thresholds of watches this hoard already stands")

	cmd.Flags().StringVarP(&o.outPath, "output", "o", "",
		"also write the interchange document to FILE")
	return cmd
}

func runMerge(a *app, path string, o mergeOpts) error {
	pr := stderrPrinter()
	res, err := action.MergeHoard(
		action.Deps{Store: a.store, Confirm: confirm}, pr.Fn(),
		action.MergeOptions{
			Source: path, Target: a.dbPath,
			ReplaceDecks: o.replaceDecks, ReplaceWatches: o.replaceWatches,
			DryRun: o.dryRun, Again: o.again,
		})
	pr.Close()
	if err != nil {
		return err
	}

	r := a.env.Report()
	if res.Upgraded {
		r.Detail("Upgraded %s to schema v%d; its pre-upgrade copy is at %s.",
			path, res.SourceVersion, res.BackupPath)
	}

	verb := dryRunVerb(o.dryRun, "Merged", "Would merge")
	r.Result("%s %d cards from %s.", verb, res.Copies+res.DeckCards, path)
	for _, name := range sortedKeys(res.PerBinder) {
		note := ""
		if slices.Contains(res.Created, name) {
			note = " (new binder)"
		}
		r.Detail("%d into %s%s", res.PerBinder[name], name, note)
	}
	if res.Decks > 0 {
		r.Detail("%d cards across %s", res.DeckCards, count(res.Decks, "deck", "decks"))
	}
	if res.Watches > 0 {
		r.Detail("%s", count(res.Watches, "watch", "watches"))
	}
	r.Detail("%s carried, card documents included.",
		count(res.Printings, "printing", "printings"))

	if res.FoldedInto != "" {
		r.Detail("The other hoard's default binder folded into %s.", res.FoldedInto)
	}
	if len(res.SkippedDecks) > 0 {
		r.Detail("%s left alone — already here from the same origin:",
			count(len(res.SkippedDecks), "deck", "decks"))
		for _, d := range res.SkippedDecks {
			r.Item(d)
		}
		r.Hint("Use --replace-decks to take the other hoard's copies instead.")
	}
	if len(res.SkippedWatches) > 0 {
		r.Detail("%s left alone — this hoard already watches them:",
			count(len(res.SkippedWatches), "watch", "watches"))
		for _, w := range res.SkippedWatches {
			r.Item(w)
		}
		r.Hint("Use --replace-watches to take the other hoard's thresholds instead.")
	}
	r.Detail("Not carried: price and buylist history, dated valuations, and price-gap records.")

	if o.outPath != "" {
		if err := writeMergeDocument(o.outPath, res.Document); err != nil {
			return err
		}
		r.Detail("Interchange document written to %s.", o.outPath)
	}
	noteDryRun(r, o.dryRun)
	return nil
}

func count(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func writeMergeDocument(path string, doc []byte) error {
	f, err := createOutput(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(doc); err != nil {
		f.Abort()
		return err
	}
	return f.Commit()
}
