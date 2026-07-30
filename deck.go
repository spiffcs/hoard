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
	"github.com/spiffcs/hoard/internal/scryfall"
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

	// Resolve every entry's identifier to a catalog card in bulk.
	idents := make([]scryfall.Identifier, len(deck.Entries))
	for i, e := range deck.Entries {
		idents[i] = e.Ident
	}
	found, _, err := scryfall.FetchCollection(ctx, idents)
	if err != nil {
		return err
	}
	if err := st.UpsertPrintings(found); err != nil {
		return err
	}

	// Finishes, keyed by the id the resolver hands back, so an entry can be
	// checked against the finishes its printing actually comes in.
	finishes := make(map[string][]string, len(found))
	for _, c := range found {
		finishes[c.ID] = c.Finishes
	}

	resolved := resolveIDs(found)
	var entries []store.Entry
	var unresolved []string
	var refinished int
	for _, e := range deck.Entries {
		id, ok := resolved[e.Ident.Key()]
		if !ok {
			unresolved = append(unresolved, e.Ident.Label())
			continue
		}
		// A decklist line with no *F* marker parses as non-foil, but precon
		// commanders and Duel Decks reprints are frequently foil-only. Storing
		// the finish the list claimed would ask for a price that cannot exist,
		// leaving the card at $0.00 no matter how often prices are refreshed.
		finish := e.Finish
		if corrected, changed := store.CorrectFinish(finish, finishes[id]); changed {
			finish = corrected
			refinished++
		}
		entries = append(entries, store.Entry{
			ScryfallID: id,
			Finish:     finish,
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
	if refinished > 0 {
		fmt.Printf("  %d recorded as foil: the list said otherwise but the printing has no non-foil.\n",
			refinished)
	}
	if len(unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(unresolved))
		for _, u := range unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}

	// Price what Scryfall could not, now rather than on some later
	// update-prices, so a freshly imported deck is worth what it is worth. This
	// only downloads when the import actually left a gap.
	return fillPriceGaps(ctx, st)
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

// resolveIDs indexes the cards the bulk lookup returned under every key form
// an Identifier might use, so an import line finds its card by whichever
// scheme addressed it. The scheme itself lives on scryfall.Identifier.Key.
func resolveIDs(cards []scryfall.Card) map[string]string {
	m := make(map[string]string, len(cards)*3)
	for _, c := range cards {
		m[c.ID] = c.ID
		m[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c.ID
		m[strings.ToLower(c.Name)] = c.ID
	}
	return m
}
