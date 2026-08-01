package main

// The add flow: turn a Scryfall URL, a typed name, or a camera scan into rows
// in the binder.

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
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
	set, number, err := scryfall.ParseCardURL(url)
	if err != nil {
		return err
	}
	// The destination resolves before the network round-trip: a mistyped
	// binder should fail in a millisecond, not after a fetch.
	target, targetName, err := binderTarget(st, binderRef)
	if err != nil {
		return err
	}
	card, err := scryfall.FetchCard(ctx, set, number)
	if err != nil {
		return err
	}
	// One mapping from the flag to a finish, made here, so the stored row and
	// the confirmation line cannot disagree.
	finish, price := "nonfoil", card.PriceUSD
	if foil {
		finish, price = "foil", card.PriceUSDFoil
	}
	if err := st.AddCardFinishTo(target, *card, finish, qty); err != nil {
		return err
	}
	fmt.Printf("Added %d× %s (%s/%s) as %s into %s — %s\n",
		qty, card.Name, card.Set, card.CollectorNumber, finish, targetName, ui.MoneyPtr(price))
	return nil
}

// binderTarget resolves --binder to a container id, defaulting to the
// default binder.
func binderTarget(st *store.Store, ref string) (int64, string, error) {
	if ref != "" {
		b, err := st.BinderByRef(ref)
		if err != nil {
			return 0, "", err
		}
		return b.ID, b.Name, nil
	}
	binders, err := st.ListBinders()
	if err != nil {
		return 0, "", err
	}
	return binders[0].ID, binders[0].Name, nil
}

// addFromList reads a pasted or exported card list — decklist-style lines —
// and adds everything to one binder. This is the non-interactive add: the
// same lines the deck importer reads, minus boards, plus the import ledger's
// protection against pasting the same list twice.
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

func addList(ctx context.Context, st *store.Store, data []byte, display, binderRef string, again bool) error {
	hash := contentHash(data)
	if err := refuseReimport(st, hash, again); err != nil {
		return err
	}
	target, targetName, err := binderTarget(st, binderRef)
	if err != nil {
		return err
	}

	entries, skipped, err := decksource.ParseLoose(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if len(skipped) > 0 {
			return fmt.Errorf("no card lines found in %s; %d lines could not be read (e.g. %s)",
				display, len(skipped), skipped[0])
		}
		return fmt.Errorf("no card lines found in %s", display)
	}

	res, err := cardResolver.Resolve(ctx, resolve.Requests(entries))
	if err != nil {
		return err
	}
	copies := 0
	adds := make([]store.CardAdd, 0, len(entries))
	for i, e := range entries {
		m := res.Matches[i]
		if !m.OK {
			continue
		}
		adds = append(adds, store.CardAdd{
			ContainerID: target, Card: m.Card, Finish: m.Finish, Quantity: e.Quantity,
		})
		copies += e.Quantity
	}
	if len(adds) > 0 {
		receipt := &store.ImportReceipt{Hash: hash, File: display, Cards: copies}
		if _, err := st.ApplyImport(receipt, nil, adds); err != nil {
			return err
		}
	}

	fmt.Printf("Added %d cards into %s (%d lines resolved).\n", copies, targetName, len(adds))
	if res.Refinished > 0 {
		fmt.Printf("  %d recorded as foil: the list said otherwise but the printing has no non-foil.\n",
			res.Refinished)
	}
	for _, s := range skipped {
		fmt.Printf("  Skipped %s\n", s)
	}
	if len(res.Unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(res.Unresolved))
		for _, u := range res.Unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}
	// Price what Scryfall could not, as import does, so the paste is worth
	// what it is worth immediately.
	if len(adds) > 0 {
		if err := fillPriceGaps(ctx, st); err != nil {
			return err
		}
	}
	if n := len(skipped) + len(res.Unresolved); n > 0 {
		return fmt.Errorf("%d lines were skipped: %w", n, errPartial)
	}
	return nil
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
	// Each confirmed card is persisted immediately; the session loops until the
	// user exits. The cascade hands back the destination it asked about — the
	// default binder when it never had to ask.
	add := func(res tui.Result) error {
		if res.ContainerID != 0 {
			return st.AddCardFinishTo(res.ContainerID, res.Card, res.Finish, res.Qty)
		}
		return st.AddCardFinish(res.Card, res.Finish, res.Qty)
	}
	// Lookups prefer the local catalog and fall through to Scryfall, so a name
	// completes instantly and offline where it can, and a card printed since the
	// last catalog build still resolves.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	sum, err := tui.Run(ctx, newSearcher(cat), add, helperScanner{}, name, dests)
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
	auto, reviewed := sum.Count("auto"), sum.Count("reviewed")+sum.Count("duplicate-confirmed")
	skipped, discarded := sum.Count("skipped"), sum.Count("discarded")
	line := fmt.Sprintf("Scan session: %d auto-added, %d reviewed", auto, reviewed)
	if skipped > 0 {
		line += fmt.Sprintf(", %d skipped", skipped)
	}
	if discarded > 0 {
		line += fmt.Sprintf(", %d discarded", discarded)
	}
	fmt.Println(line)
	for _, e := range sum.Entries {
		switch e.Kind {
		case "auto":
			fmt.Printf("  ✓ %s\n", e.Line)
		case "reviewed", "duplicate-confirmed":
			fmt.Printf("  + %s\n", e.Line)
		case "skipped":
			fmt.Printf("  - skipped %s\n", e.Line)
		case "discarded":
			fmt.Printf("  - %s\n", e.Line)
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
