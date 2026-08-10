package command

// The import command: adding a collection CSV exported from another app — or
// from hoard itself — to the binder of your choice.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
)

// importOpts are the flags, gathered so the constructor and the run half do
// not have to agree on a seven-parameter call.
type importOpts struct {
	format    string
	binderRef string
	dryRun    bool
	preserve  bool
	again     bool
}

// NewCmdImport builds `hoard import`.
func NewCmdImport(a *app) *cobra.Command {
	var o importOpts

	cmd := &cobra.Command{
		Use:     "import FILE",
		GroupID: groupInterop,
		Short:   "Add a collection CSV export (ManaBox, Moxfield, Delver Lens, hoard)",
		Example: "hoard import FILE [--binder B | --preserve-binders]\n" +
			"       [--format F] [--dry-run]",
		// Not cobra.ExactArgs(1): its "accepts 1 arg(s), received 0" says less
		// than the sentence this command has always answered with.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Usagef("import needs exactly one CSV file")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			return runImport(c.Context(), a.store, a.env, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.format, "format", "auto",
		"file format: auto (sniff the header), manabox, moxfield, delver, or hoard")
	cmd.Flags().StringVar(&o.binderRef, "binder", "",
		"add everything to this binder (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "resolve and report, but write nothing")
	cmd.Flags().BoolVar(&o.preserve, "preserve-binders", false,
		"recreate the file's own binders instead of using one destination")
	cmd.Flags().BoolVar(&o.again, "again", false,
		"import a file this hoard has already imported, adding its cards a second time")
	return cmd
}

func runImport(ctx context.Context, st *store.Store, env *cli.Env, path string, o importOpts) error {
	if o.binderRef != "" && o.preserve {
		return cli.Usagef("--binder and --preserve-binders name different destinations; choose one")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.ImportCollection(ctx,
		action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}, pr.Fn(),
		action.ImportOptions{
			Data: data, Display: path, Format: o.format,
			BinderRef: o.binderRef, Preserve: o.preserve, DryRun: o.dryRun, Again: o.again,
		})
	pr.Close()
	// A partial import still did its work; the report renders before the
	// exit code says "done, mostly". Any other error did not finish.
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	r := env.Report()
	verb := "Imported"
	if o.dryRun {
		verb = "Would import"
	}
	r.Result("%s %d cards (%s format): %d rows resolved.", verb, res.Copies, res.Format, res.Resolved)
	for _, name := range sortedKeys(res.PerBinder) {
		note := ""
		if slices.Contains(res.Created, name) {
			note = " (new binder)"
		}
		r.Detail("%d into %s%s", res.PerBinder[name], name, note)
	}
	if res.SkippedDeckRows > 0 {
		// A Warn, not a Detail: this line used to sit on stdout among the
		// per-binder counts, indented like a receipt, while it was in fact
		// the sentence "most of this file was not imported". It is the one
		// thing a scripted restore has to see, and Warn is where hoard puts
		// a partial outcome — marked, and on stderr where the caveats live.
		// It names the route rather than the command: 'hoard deck add' alone
		// could not read any file hoard wrote until --format text existed.
		r.Warn("Skipped %d deck rows: import fills binders. Restore a deck with "+
			"'hoard export --deck NAME --format text', then 'hoard deck add --file'.",
			res.SkippedDeckRows)
	}
	if res.Refinished > 0 {
		r.Detail("%d recorded as foil: the file said otherwise but the printing has no non-foil.",
			res.Refinished)
	}
	for _, field := range sortedKeys(res.Dropped) {
		r.Detail("Dropped %s on %d rows: hoard could not carry it.", field, res.Dropped[field])
	}
	if len(res.Unresolved) > 0 {
		r.Detail("%d cards could not be resolved and were skipped:", len(res.Unresolved))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	if o.dryRun {
		r.Hint("Dry run: nothing was written.")
	}
	// An import that skipped the file's decks did not restore the file, and
	// exiting 0 said it did: a backup script pointed at a canonical export
	// ran green while dropping every deck in it — 1,879 of 2,235 copies on
	// the collection this was found against. Exit 2 is hoard's existing word
	// for "done, mostly" (Run maps errPartial to it), which is exactly what
	// this is; the receipt above still prints, and the rows that did import
	// are still written. A dry run reports it too, because a rehearsal that
	// hides the outcome of the real run is not a rehearsal.
	if err == nil && res.SkippedDeckRows > 0 {
		err = fmt.Errorf("%d deck rows were skipped: %w", res.SkippedDeckRows, errPartial)
	}
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
