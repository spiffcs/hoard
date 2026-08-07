package main

// The add flow: turn a Scryfall URL, a typed name, or a camera scan into rows
// in the binder.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	foil := fs.Bool("foil", false, "add the card as foil (URL form only)")
	qty := fs.Int("qty", 1, "quantity to add (URL form only)")
	file := fs.String("file", "", "add a pasted/exported card list (a path, or - for stdin)")
	binderRef := fs.String("binder", "", "destination binder for URL and list adds (id, name, or unique fragment)")
	again := fs.Bool("again", false, "add a list this hoard has already added, doubling its cards on purpose")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if *qty < 1 {
		return fmt.Errorf("--qty must be at least 1")
	}

	// Piped input with nothing else asked for is the list path spelled
	// without flags: `pbpaste | hoard add` should just work.
	if *file == "" && len(pos) == 0 && !stdinIsTTY() {
		*file = "-"
	}
	if *file != "" {
		if len(pos) > 0 {
			return fmt.Errorf("--file takes the whole list; drop the card name arguments")
		}
		return addFromList(ctx, st, *file, *binderRef, *again)
	}

	// A Scryfall URL takes the fast non-interactive path; anything else (a card
	// name, or no argument) launches the interactive picker.
	if len(pos) == 1 && looksLikeURL(pos[0]) {
		return addByURL(ctx, st, pos[0], *foil, *qty, *binderRef)
	}
	if *binderRef != "" {
		// Silently ignoring the flag would file cards somewhere the user did
		// not say; the interactive picker owns that choice.
		return fmt.Errorf("--binder applies to URL and --file adds; the interactive picker chooses destinations itself")
	}
	return addByName(ctx, st, strings.Join(pos, " "))
}

// looksLikeURL reports whether an argument is a Scryfall card link rather than a
// card name.
func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://") || strings.Contains(arg, "scryfall.com")
}

func addByURL(ctx context.Context, st *store.Store, url string, foil bool, qty int, binderRef string) error {
	pr := stderrPrinter()
	res, err := action.AddByURL(ctx, addDeps(st), pr.Fn(),
		action.AddByURLOptions{URL: url, Foil: foil, Qty: qty, BinderRef: binderRef})
	pr.Close()
	if err != nil {
		return err
	}
	ui.NewReport().Success("Added %d× %s (%s/%s) as %s into %s · %s",
		qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, res.Binder, ui.MoneyPtr(res.PriceUSD))
	return nil
}

// addDeps is the dependency set every add path shares.
func addDeps(st *store.Store) action.Deps {
	return action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}
}

// addFromList reads a pasted or exported card list from a file or stdin.
func addFromList(ctx context.Context, st *store.Store, path, binderRef string, again bool) error {
	var data []byte
	var err error
	display := path
	if path == "-" {
		display = "stdin"
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	return addList(ctx, st, data, display, binderRef, again)
}

// addList runs the paste through the action layer and renders its report.
func addList(ctx context.Context, st *store.Store, data []byte, display, binderRef string, again bool) error {
	pr := stderrPrinter()
	res, err := action.AddList(ctx, addDeps(st), pr.Fn(),
		action.AddListOptions{Data: data, Display: display, BinderRef: binderRef, Again: again})
	pr.Close()
	// A partial paste still added what resolved; the report renders before
	// the exit code says "done, mostly". Any other error added nothing.
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}
	r := ui.NewReport()
	r.Result("Added %d cards into %s (%d lines resolved).", res.Copies, res.Binder, res.Resolved)
	if res.Refinished > 0 {
		r.Detail("%d recorded as foil: the list said otherwise but the printing has no non-foil.",
			res.Refinished)
	}
	for _, s := range res.Skipped {
		r.Detail("Skipped %s", s)
	}
	if len(res.Unresolved) > 0 {
		r.Detail("%d cards could not be resolved and were skipped:", len(res.Unresolved))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	return err
}

// storeAdder persists one cascade result: each confirmed card lands
// immediately, in the container the cascade asked about — the default
// binder when it never had to ask. Shared by the standalone `hoard add`
// cascade and the one embedded in browse.
func storeAdder(st *store.Store) tui.Adder {
	return func(res tui.Result) error {
		// A finish correction re-keys the row the scan just wrote instead of
		// adding beside it: the first look at a foil whose marker would not
		// read committed the nonfoil default, and a later look read it.
		if res.ReplacesFinish != "" && res.ReplacesFinish != res.Finish {
			if res.ContainerID != 0 {
				// The scan only ever corrects what it just wrote, which is
				// unassessed by construction: a camera cannot judge wear.
				_, err := st.MoveEntryFinish(res.ContainerID, res.Card.ID,
					res.ReplacesFinish, res.Finish, store.ConditionUnknown)
				return err
			}
			_, err := st.MoveCardFinish(res.Card.ID, res.ReplacesFinish, res.Finish)
			return err
		}
		if res.ContainerID != 0 {
			return st.AddCardFinishTo(res.ContainerID, res.Card, res.Finish, res.Qty)
		}
		return st.AddCardFinish(res.Card, res.Finish, res.Qty)
	}
}

func addByName(ctx context.Context, st *store.Store, name string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("adding by name needs an interactive terminal; " +
			"pass a Scryfall URL instead (e.g. hoard add https://scryfall.com/card/uma/7/...)")
	}
	dests, err := destinations(st)
	if err != nil {
		return err
	}
	// Lookups prefer the local catalog and fall through to Scryfall, so a name
	// completes instantly and offline where it can, and a card printed since the
	// last catalog build still resolves.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	sum, err := tui.Run(ctx, newSearcher(cat), storeAdder(st), helperScanner{}, name, dests)
	if err != nil {
		return err
	}
	printScanSummary(sum)
	return nil
}

// printScanSummary leaves the session's receipt in the terminal scrollback:
// scanning writes to the collection without a per-card confirm, so the record
// of what it did has to outlive the alternate screen.
func printScanSummary(sum tui.Summary) {
	if len(sum.Entries) == 0 {
		return
	}
	r := ui.NewReport()
	ok := r.OutEnv.OK()
	auto, reviewed := sum.Count("auto"), sum.Count("reviewed")+sum.Count("duplicate-confirmed")
	skipped, discarded := sum.Count("skipped"), sum.Count("discarded")
	line := fmt.Sprintf("Scan session: %d auto-added, %d reviewed", auto, reviewed)
	if skipped > 0 {
		line += fmt.Sprintf(", %d skipped", skipped)
	}
	if discarded > 0 {
		line += fmt.Sprintf(", %d discarded", discarded)
	}
	// Captures that held no card at all. Not listed individually — there is
	// nothing to name — but counted, so a run where the scanner was quietly
	// throwing things away does not read the same as a clean one.
	if sum.Ignored > 0 {
		line += fmt.Sprintf(", %d unreadable", sum.Ignored)
	}
	r.Result("%s", line)
	// The receipt already speaks the glyph vocabulary — ✓ auto-added, +
	// reviewed, - dropped — so only the ✓ gains the ok color; the piped
	// bytes stay exactly as they were.
	for _, e := range sum.Entries {
		switch e.Kind {
		case "auto":
			r.Detail("%s %s", ok("✓"), e.Line)
		case "reviewed", "duplicate-confirmed":
			r.Detail("+ %s", e.Line)
		case "skipped":
			r.Detail("- skipped %s", e.Line)
		case "discarded":
			r.Detail("- %s", e.Line)
		}
	}
}

// destinations lists everywhere an add can land: the binders (default first),
// then decks ranked by value — the same order the browser's left pane uses, so
// the picker reads like a place the user already knows.
func destinations(st *store.Store) ([]tui.Destination, error) {
	binders, err := st.ListBinders()
	if err != nil {
		return nil, err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return nil, err
	}
	out := make([]tui.Destination, 0, len(binders)+len(decks))
	for _, b := range binders {
		out = append(out, tui.Destination{ID: b.ID, Name: b.Name, Kind: "binder"})
	}
	for _, d := range store.DecksByValue(decks) {
		out = append(out, tui.Destination{ID: d.ID, Name: d.Name, Kind: "deck"})
	}
	return out, nil
}
