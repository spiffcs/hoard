package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// catalogDir is where the local card catalog lives.
//
// The cache directory, beside the MTGJSON bundles and deliberately nowhere near
// hoard.db. Every byte of it is re-downloadable and a collection is not, so
// losing this to eviction costs a rebuild while losing the other costs
// everything. It is also what keeps the migration runner's VACUUM INTO backup
// from copying sixty megabytes of card data on every schema change.
func catalogDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "catalog")
}

// openCatalog opens the local catalog, or returns nil if it cannot be opened.
//
// A nil catalog is a supported state, not an error: every caller falls through
// to the Scryfall API, so a machine with no writable cache directory simply
// behaves the way hoard did before the catalog existed.
func openCatalog() *catalog.Catalog {
	dir := catalogDir()
	if dir == "" {
		return nil
	}
	c, err := catalog.Open(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog unavailable, using the Scryfall API: %v\n", err)
		return nil
	}
	return c
}

// refreshCards resolves every id to a card, preferring the local catalog.
//
// This is the single place the cache policy lives. Two kinds of id still have to
// come from the API and both are bounded:
//
//   - ids the catalog has never seen, i.e. printings newer than its last build
//   - ids whose stored Scryfall document is missing, since the catalog carries
//     prices and identity but not the whole response
//
// The second set empties after one refresh rather than recurring, so the steady
// state is a single listing request and no card lookups at all.
func refreshCards(ctx context.Context, cat *catalog.Catalog, st *store.Store,
	ids []string) (found []scryfall.Card, notFound []scryfall.Identifier, fromCatalog int, err error) {
	need := ids
	if cat != nil && cat.CardCount() > 0 {
		local, err := cat.Cards(ids)
		if err != nil {
			return nil, nil, 0, err
		}
		undocumented, err := st.IDsNeedingDocuments()
		if err != nil {
			return nil, nil, 0, err
		}
		wantDoc := make(map[string]bool, len(undocumented))
		for _, id := range undocumented {
			wantDoc[id] = true
		}

		need = need[:0:0]
		for _, id := range ids {
			c, ok := local[id]
			if !ok || wantDoc[id] {
				need = append(need, id)
				continue
			}
			found = append(found, c)
		}
		fromCatalog = len(found)
	}

	if len(need) == 0 {
		return found, nil, fromCatalog, nil
	}
	idents := make([]scryfall.Identifier, len(need))
	for i, id := range need {
		idents[i] = scryfall.Identifier{ID: id}
	}
	remote, notFound, err := scryfall.FetchCollection(ctx, idents)
	if err != nil {
		return nil, nil, 0, err
	}
	return append(found, remote...), notFound, fromCatalog, nil
}

// ensureCatalog is the interim shim over action.EnsureCatalog while
// update-prices awaits its own migration: it supplies main's Deps and a
// stderr progress printer. It disappears when cmdUpdatePrices moves into
// the action layer.
func ensureCatalog(ctx context.Context, cat *catalog.Catalog) (pricesUsable bool) {
	pr := stderrPrinter()
	defer pr.Close()
	return action.EnsureCatalog(ctx, action.Deps{Catalog: cat, Confirm: confirm}, pr.Fn())
}

// stderrPrinter is the CLI's progress renderer: narration belongs on stderr
// (stdout is the data stream), updating in place only when stderr really is
// a terminal.
func stderrPrinter() *ui.Printer {
	return ui.NewPrinter(os.Stderr, isTTY(os.Stderr))
}

// confirm asks a yes/no question, defaulting to no.
//
// Anything but an explicit yes declines: the questions this asks all precede
// spending somebody's bandwidth, and the safe reading of a stray keystroke is
// "don't". A non-interactive stdin declines outright rather than blocking a
// script forever on a prompt nobody will answer. The prompt itself goes to
// stderr — it is conversation with the user, not command output, and it must
// not leak into a pipe that happens to still have a terminal on stdin.
func confirm(question string) bool {
	if !stdinIsTTY() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	var answer string
	fmt.Scanln(&answer)
	return answer == "y" || answer == "Y" || answer == "yes"
}

func cmdCatalog(ctx context.Context, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	cat := openCatalog()
	if cat == nil {
		return fmt.Errorf("no writable cache directory for the catalog")
	}
	defer cat.Close()

	switch sub {
	case "", "status":
		return catalogStatus(ctx, cat)
	case "update":
		pr := stderrPrinter()
		res, err := action.CatalogUpdate(ctx, action.Deps{Catalog: cat}, pr.Fn())
		pr.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Catalog ready: %s cards, %s on disk, built in %s.\n",
			ui.Count(res.Cards), ui.Bytes(res.Bytes), res.Took)
		return nil
	default:
		return fmt.Errorf("unknown catalog subcommand %q (want status|update)", sub)
	}
}

func catalogStatus(ctx context.Context, cat *catalog.Catalog) error {
	env := ui.Detect(os.Stdout)
	st := cat.CheckStatus(ctx)

	if st.Empty() {
		fmt.Println(env.Dim()("No catalog yet. Build it with: hoard catalog update"))
		return nil
	}
	fmt.Printf("%s cards · %s · %s\n",
		ui.Count(st.Cards), ui.Bytes(st.Bytes), cat.Path())
	fmt.Println(env.Dim()(fmt.Sprintf("Built %s from Scryfall's %s bundle.",
		st.Built.Local().Format("2 Jan 15:04"),
		st.SourceUpdated.Local().Format("2 Jan 15:04"))))

	switch {
	case !st.Checked:
		// Either the check is still fresh or the network is unreachable; both
		// mean the same thing to a reader, which is that nothing was asked.
		fmt.Println(env.Dim()("Scryfall not consulted this run."))
	case st.Stale:
		fmt.Printf("A newer bundle is available (%s). Update with: hoard catalog update\n",
			st.Remote.Local().Format("2 Jan 15:04"))
	default:
		fmt.Println(env.Dim()("Up to date with Scryfall."))
	}
	return nil
}
