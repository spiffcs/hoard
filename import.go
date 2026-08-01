package main

// The import command: adding a collection CSV exported from another app — or
// from hoard itself — to the binder of your choice.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spiffcs/hoard/internal/collsource"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
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
	hash := contentHash(data)
	if !*dryRun {
		if err := refuseReimport(st, hash, *again); err != nil {
			return err
		}
	}

	coll, err := collsource.Parse(bytes.NewReader(data), *format)
	if err != nil {
		return fmt.Errorf("%s: %w", pos[0], err)
	}

	// Deck rows in a canonical export are the decks' own contents. Imported
	// as loose cards they would inflate the binder totals, and re-importing
	// the decks themselves would then count every card twice — so they are
	// skipped, and `deck add` remains the way decks come back.
	skippedDeckRows := 0
	keptRows := coll.Rows[:0]
	for _, r := range coll.Rows {
		if r.Kind == "deck" {
			skippedDeckRows++
			continue
		}
		keptRows = append(keptRows, r)
	}
	coll.Rows = keptRows

	// Resolve every row through the shared pipeline — bulk lookup, name
	// retry for set+number pairs Scryfall does not know, finish correction.
	res, err := cardResolver.Resolve(ctx, resolve.Requests(coll.Rows))
	if err != nil {
		return err
	}

	type addition struct {
		binder string // destination binder; "" = the --binder/default target
		card   scryfall.Card
		finish string
		qty    int
	}
	var adds []addition
	for i, r := range coll.Rows {
		m := res.Matches[i]
		if !m.OK {
			continue
		}
		binder := ""
		if *preserve {
			binder = r.Binder
		}
		adds = append(adds, addition{binder: binder, card: m.Card, finish: m.Finish, qty: r.Quantity})
	}

	// Destinations. ListBinders puts the default binder first, and its
	// display names are what ManaBox-style binder columns are matched on.
	binders, err := st.ListBinders()
	if err != nil {
		return err
	}
	targetID, targetName := binders[0].ID, binders[0].Name
	if *binderRef != "" {
		b, err := st.BinderByRef(*binderRef)
		if err != nil {
			return err
		}
		targetID, targetName = b.ID, b.Name
	}
	binderIDs := make(map[string]int64, len(binders))
	for _, b := range binders {
		binderIDs[strings.ToLower(b.Name)] = b.ID
	}

	// Plan every write first, then hand the whole batch to the store as one
	// transaction: an interrupted import must be nothing rather than half,
	// because added quantities cannot be told apart from cards actually owned.
	// New binder names are deduped case-insensitively on their first spelling.
	var created []string
	spelling := make(map[string]string)
	copies := 0
	perBinder := make(map[string]int)
	cardAdds := make([]store.CardAdd, 0, len(adds))
	for _, a := range adds {
		dest, name := targetID, targetName
		newBinder := ""
		if a.binder != "" {
			key := strings.ToLower(a.binder)
			if id, ok := binderIDs[key]; ok {
				dest, name = id, a.binder
			} else {
				canonical, seen := spelling[key]
				if !seen {
					canonical = strings.TrimSpace(a.binder)
					spelling[key] = canonical
					created = append(created, canonical)
				}
				dest, name, newBinder = 0, canonical, canonical
			}
		}
		cardAdds = append(cardAdds, store.CardAdd{
			ContainerID: dest, Binder: newBinder,
			Card: a.card, Finish: a.finish, Quantity: a.qty,
		})
		copies += a.qty
		perBinder[name] += a.qty
	}
	if !*dryRun && len(cardAdds) > 0 {
		receipt := &store.ImportReceipt{Hash: hash, File: pos[0], Cards: copies}
		if _, err := st.ApplyImport(receipt, created, cardAdds); err != nil {
			return err
		}
	}

	verb := "Imported"
	if *dryRun {
		verb = "Would import"
	}
	fmt.Printf("%s %d cards (%s format): %d rows resolved.\n", verb, copies, coll.Format, len(adds))
	for _, name := range sortedKeys(perBinder) {
		note := ""
		if slices.Contains(created, name) {
			note = " (new binder)"
		}
		fmt.Printf("  %d into %s%s\n", perBinder[name], name, note)
	}
	if skippedDeckRows > 0 {
		fmt.Printf("  Skipped %d deck rows: decks come back via 'hoard deck add', not as loose cards.\n",
			skippedDeckRows)
	}
	if res.Refinished > 0 {
		fmt.Printf("  %d recorded as foil: the file said otherwise but the printing has no non-foil.\n",
			res.Refinished)
	}
	for _, field := range sortedKeys(coll.Dropped) {
		fmt.Printf("  Dropped %s on %d rows: hoard does not track it.\n", field, coll.Dropped[field])
	}
	if len(res.Unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(res.Unresolved))
		for _, u := range res.Unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}
	if *dryRun {
		fmt.Println("Dry run: nothing was written.")
		if n := len(res.Unresolved); n > 0 {
			return fmt.Errorf("%d rows would not resolve: %w", n, errPartial)
		}
		return nil
	}
	// Price what Scryfall could not, as deck add does, so the import is worth
	// what it is worth immediately.
	if err := fillPriceGaps(ctx, st); err != nil {
		return err
	}
	if n := len(res.Unresolved); n > 0 {
		return fmt.Errorf("%d rows were skipped: %w", n, errPartial)
	}
	return nil
}

// contentHash is the import ledger's identity for a batch of cards: the
// bytes, not the filename — a renamed copy of an already-imported export is
// still the same cards, and a re-pasted list is still the same paste.
func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// refuseReimport rejects content the ledger has seen, unless again says the
// doubling is intentional. Imports add quantities, so a silent re-run doubles
// every count with no visible symptom.
func refuseReimport(st *store.Store, hash string, again bool) error {
	when, cardCount, done, err := st.ImportedAt(hash)
	if err != nil {
		return err
	}
	if done && !again {
		return fmt.Errorf(
			"this content was already imported on %s (%d cards) — re-running would double every quantity.\nUse --again to add them anyway",
			when, cardCount)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
