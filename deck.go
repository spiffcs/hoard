package main

// The deck commands: importing a decklist from a URL or a text file, and
// removing a deck again.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spiffcs/hoard/internal/action"
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

	// Acquiring the deck stays here — file paths and pasted URLs are
	// frontend-shaped; everything after them is the shared capability.
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

	pr := stderrPrinter()
	res, err := action.DeckAdd(ctx, addDeps(st), pr.Fn(), deck)
	pr.Close()
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	fmt.Printf("Imported deck #%d %q (%s): %d cards resolved.\n",
		res.ID, res.Name, res.Source, res.Resolved)
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
	return err
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
