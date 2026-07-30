package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// ensureCatalog offers to build or refresh the catalog, and reports whether its
// prices can be trusted afterwards.
//
// It asks rather than downloading: seventy-odd megabytes starting because
// somebody typed a command is a surprise on a metered connection.
//
// The return value exists because declining is always allowed and a declined
// update leaves prices that are as old as the catalog. Serving those from a
// command whose whole job is refreshing prices would report success over stale
// numbers — and `confirm` declines automatically when there is no terminal, so
// a scheduled refresh would do it silently and forever.
//
// Only prices go stale this way. A printing's identity and its finishes do not
// change, so `repair-finishes` and the add cascade keep using an out-of-date
// catalog quite happily; they simply do not call this.
func ensureCatalog(ctx context.Context, cat *catalog.Catalog) (pricesUsable bool) {
	if cat == nil {
		return false
	}
	st := cat.Status(ctx)
	switch {
	case st.Empty():
		if !confirmFn(fmt.Sprintf(
			"No local card catalog yet. Download it now (%s)?", downloadSize(ctx))) {
			fmt.Fprintln(os.Stderr,
				"  using the Scryfall API; run 'hoard catalog update' to make this fast.")
			return false
		}
	case st.Checked && st.Stale:
		if !confirmFn(fmt.Sprintf(
			"A newer card catalog is available (yours is from %s). Update it (%s)?",
			st.SourceUpdated.Local().Format("2 Jan"), downloadSize(ctx))) {
			fmt.Fprintln(os.Stderr,
				"  catalog prices would be out of date, so using the Scryfall API instead.")
			return false
		}
	default:
		// Either current, or the freshness check was skipped as recent enough.
		return !st.Empty()
	}
	if err := updateCatalog(ctx, cat); err != nil {
		// A catalog that will not build is not a reason to abandon the command;
		// everything falls through to the API.
		fmt.Fprintf(os.Stderr, "catalog update failed, using the Scryfall API: %v\n", err)
		return false
	}
	return true
}

// updateCatalog rebuilds the catalog, reporting progress.
func updateCatalog(ctx context.Context, cat *catalog.Catalog) error {
	fmt.Printf("Downloading the card catalog (%s)...\n", downloadSize(ctx))
	start := time.Now()
	last := 0
	err := cat.Update(ctx, func(n int) {
		if n-last >= 25000 {
			fmt.Printf("  %s cards...\n", ui.Count(n))
			last = n
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("Catalog ready: %s cards, %s on disk, built in %s.\n",
		ui.Count(cat.CardCount()), humanBytes(cat.Bytes()),
		time.Since(start).Round(time.Second))
	return nil
}

// downloadSize describes the transfer a rebuild would cost, or "unknown size"
// when the listing cannot be read.
func downloadSize(ctx context.Context) string {
	if n := catalog.DownloadSize(ctx); n > 0 {
		return humanBytes(n)
	}
	return "unknown size"
}

// humanBytes renders a size the way a person would say it.
//
// The smallest tier is bytes rather than kilobytes so that a nonzero size never
// reads as "0 KB" — a download prompt that claims to be about to transfer
// nothing is worse than one that says an awkward number.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// confirmFn is the prompt, indirected so tests can answer it without a
// terminal. Production always uses confirm.
var confirmFn = confirm

// confirm asks a yes/no question, defaulting to no.
//
// Anything but an explicit yes declines: the questions this asks all precede
// spending somebody's bandwidth, and the safe reading of a stray keystroke is
// "don't". A non-interactive stdin declines outright rather than blocking a
// script forever on a prompt nobody will answer.
func confirm(question string) bool {
	if !stdinIsTTY() {
		return false
	}
	fmt.Printf("%s [y/N] ", question)
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
		return updateCatalog(ctx, cat)
	default:
		return fmt.Errorf("unknown catalog subcommand %q (want status|update)", sub)
	}
}

func catalogStatus(ctx context.Context, cat *catalog.Catalog) error {
	env := ui.Detect(os.Stdout)
	st := cat.Status(ctx)

	if st.Empty() {
		fmt.Println(env.Dim()("No catalog yet. Build it with: hoard catalog update"))
		return nil
	}
	fmt.Printf("%s cards · %s · %s\n",
		ui.Count(st.Cards), humanBytes(st.Bytes), cat.Path())
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
