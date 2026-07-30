package main

// The add flow: turn a Scryfall URL, a typed name, or a camera scan into rows
// in the binder.

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	foil := fs.Bool("foil", false, "add the card as foil (URL form only)")
	qty := fs.Int("qty", 1, "quantity to add (URL form only)")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if *qty < 1 {
		return fmt.Errorf("--qty must be at least 1")
	}

	// A Scryfall URL takes the fast non-interactive path; anything else (a card
	// name, or no argument) launches the interactive picker.
	if len(pos) == 1 && looksLikeURL(pos[0]) {
		return addByURL(ctx, st, pos[0], *foil, *qty)
	}
	return addByName(ctx, st, strings.Join(pos, " "))
}

// looksLikeURL reports whether an argument is a Scryfall card link rather than a
// card name.
func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://") || strings.Contains(arg, "scryfall.com")
}

func addByURL(ctx context.Context, st *store.Store, url string, foil bool, qty int) error {
	set, number, err := scryfall.ParseCardURL(url)
	if err != nil {
		return err
	}
	card, err := scryfall.FetchCard(ctx, set, number)
	if err != nil {
		return err
	}
	// One mapping from the flag to a finish, made here, so the stored row and
	// the confirmation line cannot disagree.
	finish, price := "normal", card.PriceUSD
	if foil {
		finish, price = "foil", card.PriceUSDFoil
	}
	if err := st.AddCardFinish(*card, finish, qty); err != nil {
		return err
	}
	fmt.Printf("Added %d× %s (%s/%s) as %s — %s\n",
		qty, card.Name, card.Set, card.CollectorNumber, finish, ui.MoneyPtr(price))
	return nil
}

func addByName(ctx context.Context, st *store.Store, name string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("adding by name needs an interactive terminal; " +
			"pass a Scryfall URL instead (e.g. hoard add https://scryfall.com/card/uma/7/...)")
	}
	// Each confirmed card is persisted immediately; the session loops until the
	// user exits.
	add := func(res tui.Result) error {
		return st.AddCardFinish(res.Card, res.Finish, res.Qty)
	}
	// Lookups prefer the local catalog and fall through to Scryfall, so a name
	// completes instantly and offline where it can, and a card printed since the
	// last catalog build still resolves.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	return tui.Run(ctx, newSearcher(cat), add, helperScanner{}, name)
}
