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
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// fetchCollection is scryfall.FetchCollection behind a seam, so tests can
// resolve imports against fixtures instead of the network.
var fetchCollection = scryfall.FetchCollection

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
	// Imports add quantities, so the same file twice doubles every count with
	// no visible symptom. The ledger keys on content — a renamed copy of an
	// already-imported export is still the same cards.
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if when, cardCount, done, err := st.ImportedAt(hash); err != nil {
		return err
	} else if done && !*again && !*dryRun {
		return fmt.Errorf(
			"this file's contents were already imported on %s (%d cards) — re-running would double every quantity.\nUse --again to add them anyway",
			when, cardCount)
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

	// Resolve every row against Scryfall in bulk, exactly like deck add;
	// FetchCollection batches and retries rate limits itself.
	idents := make([]scryfall.Identifier, len(coll.Rows))
	for i, r := range coll.Rows {
		idents[i] = r.Ident
	}
	found, _, err := fetchCollection(ctx, idents)
	if err != nil {
		return err
	}
	resolved := resolveIDs(found)
	cards := make(map[string]scryfall.Card, len(found))
	for _, c := range found {
		cards[c.ID] = c
	}

	// Second chance: a set+number Scryfall does not know (a vendor's set
	// code, a renumbered promo) often still names a real card. Retry those
	// rows by name before declaring them unresolved — the printing will be
	// whichever Scryfall picks, which beats losing the card entirely.
	var retry []scryfall.Identifier
	queued := make(map[string]bool)
	for _, r := range coll.Rows {
		if _, ok := resolved[r.Ident.Key()]; ok || r.Ident.Name != "" || r.Name == "" {
			continue
		}
		ident := scryfall.Identifier{Name: r.Name}
		if !queued[ident.Key()] {
			queued[ident.Key()] = true
			retry = append(retry, ident)
		}
	}
	if len(retry) > 0 {
		found2, _, err := fetchCollection(ctx, retry)
		if err != nil {
			return err
		}
		for _, c := range found2 {
			cards[c.ID] = c
		}
		for k, id := range resolveIDs(found2) {
			if _, ok := resolved[k]; !ok {
				resolved[k] = id
			}
		}
	}

	type addition struct {
		binder string // destination binder; "" = the --binder/default target
		card   scryfall.Card
		finish string
		qty    int
	}
	var adds []addition
	var unresolved []string
	var refinished int
	for _, r := range coll.Rows {
		id, ok := resolved[r.Ident.Key()]
		if !ok && r.Name != "" {
			id, ok = resolved[strings.ToLower(r.Name)]
		}
		if !ok {
			label := r.Ident.Label()
			if r.Name != "" {
				label = r.Name
			}
			unresolved = append(unresolved, label)
			continue
		}
		card := cards[id]
		// Same guard as deck add: a file claiming a finish the printing does
		// not come in would store an unpriceable entry.
		finish := r.Finish
		if corrected, changed := store.CorrectFinish(finish, card.Finishes); changed {
			finish = corrected
			refinished++
		}
		binder := ""
		if *preserve {
			binder = r.Binder
		}
		adds = append(adds, addition{binder: binder, card: card, finish: finish, qty: r.Quantity})
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
	if refinished > 0 {
		fmt.Printf("  %d recorded as foil: the file said otherwise but the printing has no non-foil.\n",
			refinished)
	}
	for _, field := range sortedKeys(coll.Dropped) {
		fmt.Printf("  Dropped %s on %d rows: hoard does not track it.\n", field, coll.Dropped[field])
	}
	if len(unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(unresolved))
		for _, u := range unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}
	if *dryRun {
		fmt.Println("Dry run: nothing was written.")
		return nil
	}
	// Price what Scryfall could not, as deck add does, so the import is worth
	// what it is worth immediately.
	return fillPriceGaps(ctx, st)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
