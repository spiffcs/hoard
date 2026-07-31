package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// newFetcher builds the MTGJSON price reader, wired to report progress on
// stderr so long waits are visible without the package deciding how to print.
func newFetcher(st *store.Store) *pricing.Fetcher {
	return pricing.New(st, pricing.DefaultCacheDir()).
		WithProgress(func(msg string) { fmt.Fprintf(os.Stderr, "  %s\n", msg) })
}

// newQuietFetcher is newFetcher without the progress wiring, for the browse
// TUI: its alt screen owns the terminal, and stderr lines printed mid-fetch
// land inside the rendered frame.
func newQuietFetcher(st *store.Store) *pricing.Fetcher {
	return pricing.New(st, pricing.DefaultCacheDir())
}

// fillPriceGaps prices what Scryfall could not, and says what happened.
func fillPriceGaps(ctx context.Context, st *store.Store) error {
	report, err := newFetcher(st).FillGaps(ctx)
	if err != nil || report.Gaps == 0 {
		return err
	}
	if report.Skipped {
		fmt.Printf("  %d cards have no price for a finish you own; "+
			"MTGJSON had none when last asked.\n", report.Gaps)
		return nil
	}
	fmt.Printf("  %d cards have no price for a finish you own; checking MTGJSON...\n", report.Gaps)
	if report.Filled <= 0 {
		fmt.Println("  no other source could price them either.")
		return nil
	}
	fmt.Printf("  filled %d from %s.\n", report.Filled, strings.Join(report.Sources, ", "))
	if report.Remaining > 0 {
		fmt.Printf("  %d still unpriced anywhere.\n", report.Remaining)
	}
	return nil
}

func cmdUpdatePrices(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("update-prices", flag.ContinueOnError)
	limit := fs.Int("limit", report.DefaultMoverRows, "risers/sinkers to list")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	ids, err := st.AllPrintingIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("No cards yet; nothing to update.")
		return nil
	}

	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	// Prices are only taken from the catalog when it is current. A stale one is
	// still fine for everything else, but this command exists to refresh prices
	// and must not report success over yesterday's.
	priceSource := cat
	if !ensureCatalog(ctx, cat) {
		priceSource = nil
	}
	found, notFound, local, err := refreshCards(ctx, priceSource, st, ids)
	if err != nil {
		return err
	}
	if err := st.UpsertPrintings(found); err != nil {
		return err
	}
	fmt.Printf("Updated prices for %d of %d cards.\n", len(found), len(ids))
	if local > 0 {
		fmt.Printf("  %d from the local catalog, %d from Scryfall.\n", local, len(found)-local)
	}
	if len(notFound) > 0 {
		fmt.Printf("  %d cards could not be re-fetched from Scryfall.\n", len(notFound))
	}
	// Scryfall's results are already committed above, so a failure in the
	// fallback pass costs nothing that was just fetched.
	if err := fillPriceGaps(ctx, st); err != nil {
		return err
	}

	// After the gap fill, not before: a card priced from MTGJSON this run has
	// its effective price only once that pass has committed, and recording
	// first would log the gap and call the fill a change on the next run.
	changes, err := st.RecordPrices()
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Print(report.Movers(ui.Detect(os.Stdout), changes, *limit, "since the last refresh"))
	return nil
}

// cmdUnpriced lists what is contributing nothing to your totals.
//
// A card with no price is valued at zero, which is indistinguishable on a table
// from a card genuinely worth nothing. This is how you tell the difference, and
// how you find out which deck's total is understated.
func cmdUnpriced(st *store.Store) error {
	rows, err := st.Unpriced()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	fmt.Print(report.Unpriced(env, rows))
	if len(rows) > 0 {
		fmt.Print(report.UnpricedAdvice(env))
	}
	return nil
}
