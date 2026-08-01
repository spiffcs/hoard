package main

// The import command: adding a collection CSV exported from another app — or
// from hoard itself — to the binder of your choice.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
)

func cmdImport(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	format := fs.String("format", "auto", "file format: auto (sniff the header), manabox, moxfield, delver, or hoard")
	binderRef := fs.String("binder", "", "add everything to this binder (id, name, or unique fragment)")
	dryRun := fs.Bool("dry-run", false, "resolve and report, but write nothing")
	preserve := fs.Bool("preserve-binders", false, "recreate the file's own binders instead of using one destination")
	again := fs.Bool("again", false, "import a file this hoard has already imported, adding its cards a second time")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("import needs exactly one CSV file")
	}
	if *binderRef != "" && *preserve {
		return fmt.Errorf("--binder and --preserve-binders name different destinations; choose one")
	}

	data, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.ImportCollection(ctx,
		action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}, pr.Fn(),
		action.ImportOptions{
			Data: data, Display: pos[0], Format: *format,
			BinderRef: *binderRef, Preserve: *preserve, DryRun: *dryRun, Again: *again,
		})
	pr.Close()
	// A partial import still did its work; the report renders before the
	// exit code says "done, mostly". Any other error did not finish.
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	verb := "Imported"
	if *dryRun {
		verb = "Would import"
	}
	fmt.Printf("%s %d cards (%s format): %d rows resolved.\n", verb, res.Copies, res.Format, res.Resolved)
	for _, name := range sortedKeys(res.PerBinder) {
		note := ""
		if slices.Contains(res.Created, name) {
			note = " (new binder)"
		}
		fmt.Printf("  %d into %s%s\n", res.PerBinder[name], name, note)
	}
	if res.SkippedDeckRows > 0 {
		fmt.Printf("  Skipped %d deck rows: decks come back via 'hoard deck add', not as loose cards.\n",
			res.SkippedDeckRows)
	}
	if res.Refinished > 0 {
		fmt.Printf("  %d recorded as foil: the file said otherwise but the printing has no non-foil.\n",
			res.Refinished)
	}
	for _, field := range sortedKeys(res.Dropped) {
		fmt.Printf("  Dropped %s on %d rows: hoard does not track it.\n", field, res.Dropped[field])
	}
	if len(res.Unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(res.Unresolved))
		for _, u := range res.Unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}
	if *dryRun {
		fmt.Println("Dry run: nothing was written.")
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
