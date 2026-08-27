package command

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

var importFormats = []string{"auto", "manabox", "moxfield", "delver", "hoard"}

type importOpts struct {
	format    string
	binderRef string
	dryRun    bool
	preserve  bool
	again     bool
}

func NewCmdImport(a *app) *cobra.Command {
	var o importOpts

	cmd := &cobra.Command{
		Use:     "import FILE|-",
		GroupID: groupInterop,

		Short: "Add a collection CSV from another app, or from hoard",

		Long: "Adds a collection CSV to a binder. It recognises exports\n" +
			"from ManaBox, Moxfield, Delver Lens and hoard itself;\n" +
			"--format names the format when the header does not.\n\n" +
			"Every format here is a CSV dialect: --format hoard means\n" +
			"the CSV hoard exports, not the JSON document of the same\n" +
			"name, which import does not read.\n\n" +

			"Cards round-trip exactly; value is re-derived. No import\n" +
			"reads a price from a file — prices come from the local\n" +
			"catalog as each card resolves — so re-importing an export\n" +
			"values it at today's prices, not the ones in its Price\n" +
			"USD column.",
		Example: "hoard import FILE [--binder B | --preserve-binders]\n" +
			"       [--format F] [--dry-run]\n" +
			"pbpaste | hoard import -",

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

	if !slices.Contains(importFormats, o.format) {
		return cli.Usagef("unknown format %q (want %s)", o.format, strings.Join(importFormats, ", "))
	}

	data, display, err := readPathOrStdin(path)
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.ImportCollection(ctx,
		priced(action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}),
		pr.Fn(),
		action.ImportOptions{
			Data: data, Display: display, Format: o.format,
			BinderRef: o.binderRef, Preserve: o.preserve, DryRun: o.dryRun, Again: o.again,
		})
	pr.Close()

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

	if err == nil && res.SkippedDeckRows > 0 {
		err = fmt.Errorf("%d deck rows were skipped: %w", res.SkippedDeckRows, errPartial)
	}
	return err
}

func dryRunVerb(dryRun bool, did, would string) string {
	if dryRun {
		return would
	}
	return did
}

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
