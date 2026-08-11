package command

// The import command: adding a collection CSV exported from another app — or
// from hoard itself — to the binder of your choice.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// importFormats is every value --format accepts, in the order the help and the
// error text list them.
//
// A restatement of collsource's own spec table, which is unexported, plus the
// sniff pseudo-format that table has no row for. The duplication is guarded by
// a test that asks collsource to accept each name, so this list cannot come to
// promise a parser that does not exist; the other direction — a dialect added
// there and not named here — is a change to that package, made by someone with
// this line in their diff.
var importFormats = []string{"auto", "manabox", "moxfield", "delver", "hoard"}

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
		Use:     "import FILE|-",
		GroupID: groupInterop,
		// Short is 60 columns or fewer because it is what this command's own
		// page prints when there is no Long, verbatim and unwrapped. The
		// sources moved into Long rather than being dropped: which apps hoard
		// can read is the question this command gets asked, and the root
		// table — where Short is truncated to fit a column — was never the
		// place that answered it.
		Short: "Add a collection CSV from another app, or from hoard",
		// The second paragraph exists because the first one's last word is a
		// trap: hoard names both a CSV dialect and the JSON interchange
		// document, and --format offers only the first. An agent reading this
		// page would otherwise reasonably try --format hoard on a .json file
		// and spend ten minutes on the answer.
		Long: "Adds a collection CSV to a binder. It recognises exports\n" +
			"from ManaBox, Moxfield, Delver Lens and hoard itself;\n" +
			"--format names the format when the header does not.\n\n" +
			"Every format here is a CSV dialect: --format hoard means\n" +
			"the CSV hoard exports, not the JSON document of the same\n" +
			"name, which import does not read.\n\n" +
			// The round trip is card-exact and value-approximate, and
			// only the first half was ever stated. Re-exporting a
			// hoard export came back a few cents off — 34 on a 915-card
			// collection, one card whose price had moved — which is
			// correct behaviour that looks exactly like a rounding bug
			// if you have not been told which of the two it is. Said
			// here rather than in the receipt because the difference is
			// a rounding error's size, and a line of runtime output on
			// every import would be a louder claim than the facts
			// support.
			"Cards round-trip exactly; value is re-derived. No import\n" +
			"reads a price from a file — prices come from the local\n" +
			"catalog as each card resolves — so re-importing an export\n" +
			"values it at today's prices, not the ones in its Price\n" +
			"USD column.",
		Example: "hoard import FILE [--binder B | --preserve-binders]\n" +
			"       [--format F] [--dry-run]\n" +
			"pbpaste | hoard import -",
		// Not cobra.ExactArgs(1): its "accepts 1 arg(s), received 0" says less
		// than the sentence this command has always answered with.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Usagef("import needs exactly one CSV file (or - for stdin)")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			return runImport(c.Context(), a.store, a.env, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.format, "format", "auto",
		"CSV dialect: auto (sniff the header), manabox, moxfield, delver, or hoard (hoard's own CSV, not its JSON)")
	cmd.Flags().StringVar(&o.binderRef, "binder", "",
		"add everything to this binder (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "resolve and report, but write nothing")
	// "creating any this hoard does not have" is the half that was never said,
	// and it is the half that surprises: --binder refuses a name it cannot
	// match, so a reader who has met that refusal has been taught that import
	// does not make binders. This flag does, and the only place that was
	// visible was the receipt, after the fact.
	cmd.Flags().BoolVar(&o.preserve, "preserve-binders", false,
		"recreate the file's own binders, creating any this hoard does not have, instead of using one destination")
	cmd.Flags().BoolVar(&o.again, "again", false,
		"import a file this hoard has already imported, adding its cards a second time")
	return cmd
}

func runImport(ctx context.Context, st *store.Store, env *cli.Env, path string, o importOpts) error {
	if o.binderRef != "" && o.preserve {
		return cli.Usagef("--binder and --preserve-binders name different destinations; choose one")
	}
	// Before the file is opened, not after it is parsed. collsource already
	// refuses a format it has no spec for — but only from inside Parse, which
	// lexes the whole file as CSV first, so a value like json handed a JSON
	// file died on a bare quote in line 2 and sent the reader hunting a
	// quoting problem that was not there. The flag is wrong whatever the file
	// turns out to hold, and that is the cheaper, more certain diagnosis.
	//
	// Shaped after schema --kind, which is the tree's answer to this question:
	// name the value that could not be honoured, then list the ones that can.
	if !slices.Contains(importFormats, o.format) {
		return cli.Usagef("unknown format %q (want %s)", o.format, strings.Join(importFormats, ", "))
	}

	// A lone dash is stdin, spelled the way add --file spells it, so a
	// collection can be piped from wherever it was generated. ImportOptions
	// has documented its Display as a path or stdin since it was written;
	// only this line never delivered the second half.
	data, display, err := readPathOrStdin(path)
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.ImportCollection(ctx,
		action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}, pr.Fn(),
		action.ImportOptions{
			Data: data, Display: display, Format: o.format,
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
		// could not read any file hoard wrote until --format text existed,
		// and the two halves could not be piped together until --file -
		// existed. Now that they can, the advice is the one line a user can
		// paste rather than two commands and a temporary file between them.
		r.Warn("Skipped %d deck rows: import fills binders. Restore a deck with "+
			"'hoard export --deck NAME --format text | hoard deck add --file - --name NAME'.",
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
