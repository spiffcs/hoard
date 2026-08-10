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
	"github.com/spiffcs/hoard/internal/ui"
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
		// Short is 60 columns or fewer because it is what this command's own
		// page prints when there is no Long, verbatim and unwrapped. The
		// sources moved into Long rather than being dropped: which apps hoard
		// can read is the question this command gets asked, and the root
		// table — where Short is truncated to fit a column — was never the
		// place that answered it.
		Short: "Add a collection CSV from another app, or from hoard",
		Long: "Adds a collection CSV to a binder. It recognises exports\n" +
			"from ManaBox, Moxfield, Delver Lens and hoard itself;\n" +
			"--format names the format when the header does not.",
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
	verb := dryRunVerb(o.dryRun, "Imported", "Would import")
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
	noteDryRun(r, o.dryRun)
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

// dryRunVerb picks a headline's verb: the past tense when the command wrote,
// the conditional when it only rehearsed.
//
// Here rather than in each command because there are three rehearsable
// commands and this is the shape all of them wanted — `import` and `merge`
// spell it exactly this way, and `deck add` differs only in that its two
// headlines are not the same sentence with one word swapped (a real run names
// the deck id the rehearsal has not got), so it keeps its own branch.
func dryRunVerb(dryRun bool, did, would string) string {
	if dryRun {
		return would
	}
	return did
}

// noteDryRun closes a rehearsal's receipt with the line that says so.
//
// One copy, deliberately. `import`, `merge` and `deck add` each grew their own
// literal — the second and third written by agents working in parallel who
// could not reach the first — and three literals of a sentence this load-
// bearing is three chances for a user to be told "nothing was written" in a
// wording that does not match the last command that told them so. The
// receipt's last line is where a reader looks to find out whether their
// collection changed; it should read identically wherever they got it.
//
// Nothing else in this file is where the caller expects it either — sortedKeys
// is here and used from three files — so this is the established home for
// helpers the interop commands share.
func noteDryRun(r *ui.Report, dryRun bool) {
	if dryRun {
		r.Hint("Dry run: nothing was written.")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
