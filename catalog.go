package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/catalog"
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
	if c.ReplacedOutdated() {
		// The schema bump wiped a populated catalog; without this line the
		// next download prompt reads as "your catalog vanished".
		ui.NewReport().Progress(
			"The local catalog predates this hoard's format; the next update rebuilds it in full.")
	}
	return c
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
// A non-interactive stdin declines outright rather than blocking a script
// forever on a prompt nobody will answer. The prompt itself goes to stderr —
// it is conversation with the user, not command output, and it must not
// leak into a pipe that happens to still have a terminal on stdin. The ask
// itself is ui.Confirm, the same [y/N] every confirm in hoard speaks.
func confirm(question string) bool {
	if !stdinIsTTY() {
		return false
	}
	ok, err := ui.Confirm(os.Stdin, os.Stderr, question)
	return err == nil && ok
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
		ui.NewReport().Progress("Catalog ready: %s cards, %s on disk, built in %s.",
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
