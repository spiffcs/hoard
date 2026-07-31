package main

// The deck commands: importing a decklist from a URL or a text file, and
// removing a deck again.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/store"
)

func cmdDeck(ctx context.Context, st *store.Store, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("deck requires a subcommand: add|remove")
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "add":
		return cmdDeckAdd(ctx, st, subArgs)
	case "remove":
		return cmdDeckRemove(st, subArgs)
	default:
		return fmt.Errorf("unknown deck subcommand %q (want add|remove)", sub)
	}
}

func cmdDeckAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("deck add", flag.ContinueOnError)
	file := fs.String("file", "", "import from a text/exported decklist file instead of a URL")
	name := fs.String("name", "", "deck name (defaults to the file name for --file imports)")
	source := fs.String("source", "", "provider label for text imports (e.g. moxfield)")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}

	var deck *decksource.Deck
	if *file != "" {
		deck, err = importTextDeck(*file, *name, *source)
	} else if len(pos) == 1 {
		deck, err = decksource.Fetch(ctx, pos[0])
	} else {
		return fmt.Errorf("deck add needs either a deck URL or --file <path>")
	}
	if err != nil {
		return err
	}

	// Resolve every entry in bulk — the shared pipeline also retries misses
	// by name and corrects finishes the printing does not come in (a decklist
	// with no *F* marker parses as non-foil, but precon commanders are
	// frequently foil-only, and the claimed finish would price at $0 forever).
	reqs := make([]resolve.Request, len(deck.Entries))
	for i, e := range deck.Entries {
		reqs[i] = resolve.Request{Ident: e.Ident, Name: e.Name, Finish: e.Finish}
	}
	res, err := cardResolver.Resolve(ctx, reqs)
	if err != nil {
		return err
	}
	if err := st.UpsertPrintings(res.Found); err != nil {
		return err
	}

	var entries []store.Entry
	for i, e := range deck.Entries {
		m := res.Matches[i]
		if !m.OK {
			continue
		}
		entries = append(entries, store.Entry{
			ScryfallID: m.Card.ID,
			Finish:     m.Finish,
			Board:      e.Board,
			Quantity:   e.Quantity,
		})
	}

	id, err := st.UpsertDeck(store.DeckMeta{
		Name:      deck.Name,
		Source:    deck.Source,
		SourceID:  deck.SourceID,
		SourceURL: deck.SourceURL,
		Format:    deck.Format,
	}, entries)
	if err != nil {
		return err
	}

	fmt.Printf("Imported deck #%d %q (%s): %d cards resolved.\n",
		id, deck.Name, deck.Source, len(entries))
	if res.Refinished > 0 {
		fmt.Printf("  %d recorded as foil: the list said otherwise but the printing has no non-foil.\n",
			res.Refinished)
	}
	if len(res.Unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(res.Unresolved))
		for _, u := range res.Unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}

	// Price what Scryfall could not, now rather than on some later
	// update-prices, so a freshly imported deck is worth what it is worth. This
	// only downloads when the import actually left a gap.
	if err := fillPriceGaps(ctx, st); err != nil {
		return err
	}
	if n := len(res.Unresolved); n > 0 {
		return fmt.Errorf("%d cards were skipped: %w", n, errPartial)
	}
	return nil
}

func importTextDeck(path, name, source string) (*decksource.Deck, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return decksource.ParseText(name, "", "", source, f)
}

func cmdDeckRemove(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("deck remove requires a deck id or name")
	}
	deck, err := st.DeckByRef(args[0])
	if err != nil {
		return err
	}
	if _, err := st.RemoveContainer(deck.ID); err != nil {
		return err
	}
	fmt.Printf("Removed deck #%d %q\n", deck.ID, deck.Name)
	return nil
}

// cardResolver is the shared import pipeline (bulk lookup, name retry, finish
// correction). One instance so tests can swap its Fetch for a fixture-backed
// lookup and cover deck add and import through the same seam.
var cardResolver = &resolve.Resolver{}
