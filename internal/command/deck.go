package command

// The deck commands: importing a decklist from a URL or a text file, re-pinning
// it to the set it came from, and removing it again.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/store"
)

// NewCmdDeck builds `hoard deck`, a group with no bare form: unlike binder and
// watch, there is no one thing "deck" on its own should mean.
func NewCmdDeck(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deck",
		GroupID: groupDeck,
		Short:   "Import, refresh and remove decks",
		Example: "hoard deck add <archidekt-url> [--refresh]\n" +
			"hoard deck add --file <path> [--name NAME] [--source S]\n" +
			"hoard deck remove <name>\n" +
			"hoard deck repin <name> <set>",
		Args: cobra.NoArgs,
		// An error, not cmd.Usage(): `hoard deck` alone has always been a
		// mistake rather than a request, and it exits non-zero accordingly.
		// The list now names repin, which it has always accepted.
		RunE: func(*cobra.Command, []string) error {
			return cli.Usagef("deck requires a subcommand: add|remove|repin")
		},
	}
	cmd.AddCommand(newDeckAddCmd(a), newDeckRemoveCmd(a), newDeckRepinCmd(a))
	return cmd
}

func newDeckAddCmd(a *app) *cobra.Command {
	var file, name, source string
	var refresh bool

	cmd := &cobra.Command{
		Use:   "add [URL]",
		Short: "Import a decklist from a URL or a file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runDeckAdd(c.Context(), a.store, a.env, args, file, name, source, refresh)
		},
	}
	cmd.Flags().StringVar(&file, "file", "",
		"import from a text/exported decklist file instead of a URL")
	cmd.Flags().StringVar(&name, "name", "",
		"deck name (defaults to the file name for --file imports)")
	cmd.Flags().StringVar(&source, "source", "",
		"provider label for text imports (e.g. moxfield)")
	cmd.Flags().BoolVar(&refresh, "refresh", false,
		"replace an already-imported deck without asking (discards manual edits to its cards)")
	return cmd
}

func runDeckAdd(ctx context.Context, st *store.Store, env *cli.Env, args []string,
	file, name, source string, refresh bool) error {
	// Acquiring the deck stays here — file paths and pasted URLs are
	// frontend-shaped; everything after them is the shared capability.
	var deck *decksource.Deck
	var err error
	switch {
	case file != "":
		deck, err = importTextDeck(file, name, source)
	case len(args) == 1:
		deck, err = decksource.Fetch(ctx, args[0])
	default:
		return cli.Usagef("deck add needs either a deck URL or --file <path>")
	}
	if err != nil {
		return err
	}

	// The re-import gate: an existing deck's entries are replaced wholesale,
	// so DeckAdd asks first. --refresh pre-answers for scripts; otherwise
	// the terminal gets the same [y/N] every confirm in hoard speaks.
	deps := addDeps(st)
	if refresh {
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

	r := env.Report()
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

func newDeckRepinCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "repin DECK SET",
		Short: "Re-point a deck's cards at the set it came from",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return cli.Usagef("deck repin requires a deck (id or name) and a set code, " +
					"like: deck repin \"guided by nature\" cma")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			return runDeckRepin(c.Context(), a.store, a.env, args[0], args[1])
		},
	}
}

// runDeckRepin re-points a deck's cards at the set it actually came from.
// Name-only imports resolve to arbitrary printings — typically the newest —
// which misattributes a precon's cards to sets it was never part of.
func runDeckRepin(ctx context.Context, st *store.Store, env *cli.Env, deckRef, setCode string) error {
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	res, err := action.RepinDeck(ctx, st, newSearcher(cat), deckRef, setCode)
	if err != nil {
		return err
	}

	r := env.Report()
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

func newDeckRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove DECK",
		Short: "Remove a deck and its cards",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Usagef("deck remove requires a deck id or name")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return runDeckRemove(a.store, a.env, args[0])
		},
	}
}

func runDeckRemove(st *store.Store, env *cli.Env, ref string) error {
	deck, err := st.DeckByRef(ref)
	if err != nil {
		return err
	}
	if _, err := st.RemoveContainer(deck.ID); err != nil {
		return err
	}
	env.Report().Success("Removed deck #%d %q", deck.ID, deck.Name)
	return nil
}

// cardResolver is the shared import pipeline (bulk lookup, name retry, finish
// correction). One instance so tests can swap its Fetch for a fixture-backed
// lookup and cover deck add and import through the same seam.
var cardResolver = &resolve.Resolver{}
