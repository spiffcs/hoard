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
	"github.com/spiffcs/hoard/internal/ui"
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
	case "repin":
		return cmdDeckRepin(ctx, st, subArgs)
	default:
		return fmt.Errorf("unknown deck subcommand %q (want add|remove|repin)", sub)
	}
}

func cmdDeckAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("deck add", flag.ContinueOnError)
	file := fs.String("file", "", "import from a text/exported decklist file instead of a URL")
	name := fs.String("name", "", "deck name (defaults to the file name for --file imports)")
	source := fs.String("source", "", "provider label for text imports (e.g. moxfield)")
	refresh := fs.Bool("refresh", false, "replace an already-imported deck without asking (discards manual edits to its cards)")
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

	// The re-import gate: an existing deck's entries are replaced wholesale,
	// so DeckAdd asks first. --refresh pre-answers for scripts; otherwise
	// the terminal gets the same [y/N] every confirm in hoard speaks.
	deps := addDeps(st)
	if *refresh {
		deps.Confirm = func(string) bool { return true }
	} else {
		deps.Confirm = confirm
	}
	pr := stderrPrinter()
	res, err := action.DeckAdd(ctx, deps, pr.Fn(), deck)
	pr.Close()
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	r := ui.NewReport()
	r.Result("Imported deck #%d %q (%s): %d cards resolved.",
		res.ID, res.Name, res.Source, res.Resolved)
	if res.Refinished > 0 {
		r.Detail("%d recorded as foil: the list said otherwise but the printing has no non-foil.",
			res.Refinished)
	}
	if len(res.Unresolved) > 0 {
		r.Detail("%d cards could not be resolved and were skipped:", len(res.Unresolved))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	if len(deck.Skipped) > 0 {
		r.Detail("%d lines could not be read and were skipped:", len(deck.Skipped))
		for _, sk := range deck.Skipped {
			r.Item(sk)
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

// cmdDeckRepin re-points a deck's cards at the set it actually came from.
// Name-only imports resolve to arbitrary printings — typically the newest —
// which misattributes a precon's cards to sets it was never part of.
func cmdDeckRepin(ctx context.Context, st *store.Store, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("deck repin requires a deck (id or name) and a set code, like: deck repin \"guided by nature\" cma")
	}
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	res, err := action.RepinDeck(ctx, st, newSearcher(cat), args[0], args[1])
	if err != nil {
		return err
	}

	r := ui.NewReport()
	if res.Repinned == 0 && len(res.Missing) == 0 {
		r.Success("Deck #%d %q is already on %s (%d printings).",
			res.DeckID, res.Deck, strings.ToUpper(res.SetCode), res.Total)
		return nil
	}
	r.Result("Re-pinned deck #%d %q to %s: %d of %d printings moved, %d already there.",
		res.DeckID, res.Deck, strings.ToUpper(res.SetCode), res.Repinned, res.Total, res.Already)
	if res.Repinned > 0 {
		r.Detail("Run `hoard update-prices` to price the corrected printings.")
	}
	if len(res.Missing) > 0 {
		r.Detail("%d cards have no printing in %s and were left untouched:",
			len(res.Missing), strings.ToUpper(res.SetCode))
		for _, name := range res.Missing {
			r.Item(name)
		}
	}
	return nil
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
	ui.NewReport().Success("Removed deck #%d %q", deck.ID, deck.Name)
	return nil
}

// cardResolver is the shared import pipeline (bulk lookup, name retry, finish
// correction). One instance so tests can swap its Fetch for a fixture-backed
// lookup and cover deck add and import through the same seam.
var cardResolver = &resolve.Resolver{}
