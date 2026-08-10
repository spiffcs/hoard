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
		// --dry-run gets a line of its own rather than a fourth bracket on the
		// --file form: that line is already 57 of the 58 columns a 60-column
		// terminal leaves after the indent. A continuation line was the other
		// option — import wraps that way — but the renderer re-indents every
		// example to two spaces, so a continuation arrives looking like a form
		// with the command name missing. Better a short real one. It is shown
		// on the --file form because that is the workflow it is for, the same
		// way --refresh is shown only on the URL form though both accept it.
		Example: "hoard deck add <archidekt-url> [--refresh]\n" +
			"hoard deck add --file <path> [--name NAME] [--source S]\n" +
			"hoard deck add --file <path> --dry-run\n" +
			"... | hoard deck add --file - --name NAME\n" +
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

// deckAddOpts are the flags, gathered as import's are so the constructor and
// the run half do not have to agree on a nine-parameter call.
type deckAddOpts struct {
	file    string
	name    string
	source  string
	refresh bool
	dryRun  bool
}

func newDeckAddCmd(a *app) *cobra.Command {
	var o deckAddOpts

	cmd := &cobra.Command{
		Use:   "add [URL]",
		Short: "Import a decklist from a URL or a file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runDeckAdd(c.Context(), a.store, a.env, args, o)
		},
	}
	cmd.Flags().StringVar(&o.file, "file", "",
		"import a decklist instead of a URL (a path, or - for stdin)")
	cmd.Flags().StringVar(&o.name, "name", "",
		"deck name (defaults to the file name; required when --file is -)")
	cmd.Flags().StringVar(&o.source, "source", "",
		"provider label for text imports (e.g. moxfield)")
	cmd.Flags().BoolVar(&o.refresh, "refresh", false,
		"replace an already-imported deck without asking (discards manual edits to its cards)")
	// Worded exactly as import's, because it is the same promise: the list is
	// fetched and resolved for real — only the writing is withheld.
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "resolve and report, but write nothing")
	return cmd
}

func runDeckAdd(ctx context.Context, st *store.Store, env *cli.Env, args []string, o deckAddOpts) error {
	// Acquiring the deck stays here — file paths and pasted URLs are
	// frontend-shaped; everything after them is the shared capability. A dry
	// run still acquires: a URL that 404s or a file that will not parse is
	// exactly what the rehearsal is meant to find out.
	var deck *decksource.Deck
	var err error
	switch {
	case o.file == "-":
		deck, err = readDeckFromStdin(o.name, o.source)
	case o.file != "":
		deck, err = importTextDeck(o.file, o.name, o.source)
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
	// the terminal gets the same [y/N] every confirm in hoard speaks. A dry
	// run is never asked at all — DeckAdd reports the collision instead —
	// so --refresh alongside it is redundant rather than contradictory, and
	// is accepted in silence the way import accepts --dry-run --again.
	deps := addDeps(st)
	if o.refresh {
		deps.Confirm = func(string) bool { return true }
	} else {
		deps.Confirm = confirm
	}
	pr := stderrPrinter()
	res, err := action.DeckAdd(ctx, deps, pr.Fn(), deck, action.DeckAddOptions{DryRun: o.dryRun})
	pr.Close()
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	r := env.Report()
	// No "#%d" on a rehearsal: the deck was never created, so there is no id
	// to name, and printing the zero one would invent a row that does not
	// exist. Below the headline the two paths say the same things, except
	// for the replacement warning only a rehearsal is in a position to give.
	if o.dryRun {
		r.Result("Would import deck %q (%s): %d cards resolved.", res.Name, res.Source, res.Resolved)
	} else {
		r.Result("Imported deck #%d %q (%s): %d cards resolved.",
			res.ID, res.Name, res.Source, res.Resolved)
	}
	if res.Replaces != "" {
		r.Detail("Would replace the imported deck %q, discarding manual edits to its cards.",
			res.Replaces)
	}
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
	noteDryRun(r, o.dryRun)
	return err
}

// readDeckFromStdin reads a decklist from a pipe, so the round trip import
// recommends — export a deck to text, read it back — composes as one line
// instead of needing a temporary file between the halves.
//
// The dash is refused without --name, and that is the decision this function
// exists to hold. Everywhere else a dash is a straight substitution for the
// path, but here the path is not only where the bytes come from: it is also
// what the deck is called, and a deck's name is not cosmetic — deck remove
// and deck repin take one, and browse lists it. A pipe carries no file name
// and a decklist has no name inside it (export writes board headers, not a
// title), so a dash would have to invent one. Naming a deck "-", or "stdin",
// files it under something the user never said, which is the objection
// add --binder already makes on the path that cannot honour it. --name
// already exists for exactly this, so the refusal costs a flag rather than a
// feature.
//
// Separate from importTextDeck deliberately: browse's file prompt calls that
// one, and inside the TUI a dash names no stream the browser does not
// already own.
func readDeckFromStdin(name, source string) (*decksource.Deck, error) {
	if strings.TrimSpace(name) == "" {
		return nil, cli.Usagef("reading a decklist from stdin needs --name: " +
			"a pipe carries no file name to call the deck")
	}
	return decksource.ParseText(name, "", "", source, os.Stdin)
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
