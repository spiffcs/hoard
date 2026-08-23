package command

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
	"github.com/spiffcs/hoard/internal/ui"
)

func NewCmdDeck(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deck",
		GroupID: groupDeck,
		Short:   "Import, refresh and remove decks",

		Example: "hoard deck add <archidekt-url> [--refresh]\n" +
			"hoard deck add --file <path> [--name NAME] [--source S]\n" +
			"hoard deck add --file <path> --dry-run\n" +
			"... | hoard deck add --file - --name NAME\n" +
			"hoard deck remove <name>\n" +
			"hoard deck move <name> <folder>\n" +
			"hoard deck repin <name> <set>",
		Args: cobra.NoArgs,

		RunE: func(*cobra.Command, []string) error {
			return cli.Usagef("deck requires a subcommand: add|remove|repin")
		},
	}
	cmd.AddCommand(newDeckAddCmd(a), newDeckRemoveCmd(a), newDeckRepinCmd(a), newDeckMoveCmd(a))
	return cmd
}

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

	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "resolve and report, but write nothing")
	return cli.Mutating(cmd)
}

func runDeckAdd(ctx context.Context, st *store.Store, env *cli.Env, args []string, o deckAddOpts) error {

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

	if o.dryRun {
		r.Result("Would import deck %q (%s): %s resolved.",
			res.Name, res.Source, ui.Plural(res.Resolved, "card", "cards"))
	} else {
		r.Result("Imported deck #%d %q (%s): %s resolved.",
			res.ID, res.Name, res.Source, ui.Plural(res.Resolved, "card", "cards"))
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
		r.Detail("%s", unresolvedHeading(len(res.Unresolved)))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	if len(deck.Skipped) > 0 {
		r.Detail("%s", unreadableHeading(len(deck.Skipped)))
		for _, sk := range deck.Skipped {
			r.Item(sk)
		}
	}
	noteDryRun(r, o.dryRun)
	return err
}

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
	return cli.Mutating(&cobra.Command{
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
	})
}

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
		r.Success("Deck #%d %q is already on %s (%s).",
			res.DeckID, res.Deck, strings.ToUpper(res.SetCode),
			ui.Plural(res.Total, "printing", "printings"))
		return nil
	}
	r.Result("Re-pinned deck #%d %q to %s: %d of %d printings moved, %d already there.",
		res.DeckID, res.Deck, strings.ToUpper(res.SetCode), res.Repinned, res.Total, res.Already)
	if res.Repinned > 0 {
		r.Detail("Run `hoard update-prices` to price the corrected printings.")
	}
	if len(res.Missing) > 0 {

		left := "were left untouched:"
		if len(res.Missing) == 1 {
			left = "was left untouched:"
		}
		r.Detail("%s no printing in %s and %s",
			ui.Plural(len(res.Missing), "card has", "cards have"),
			strings.ToUpper(res.SetCode), left)
		for _, name := range res.Missing {
			r.Item(name)
		}
	}
	return nil
}

func newDeckRemoveCmd(a *app) *cobra.Command {
	return cli.Mutating(&cobra.Command{
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
	})
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

var cardResolver = &resolve.Resolver{}
