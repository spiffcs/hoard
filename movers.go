package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdMovers(st *store.Store, args []string) error {
	fs := flag.NewFlagSet("movers", flag.ContinueOnError)
	since := fs.String("since", "30d", "how far back to compare (e.g. 7d, 2w, 48h)")
	limit := fs.Int("limit", report.DefaultMoverRows, "rows per section")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}
	window, err := parseWindow(*since)
	if err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	observations, oldest, err := st.PriceHistoryDepth()
	if err != nil {
		return err
	}
	// An empty result means "nothing moved", which is indistinguishable from
	// "nothing was ever recorded" unless the difference is stated outright.
	if observations == 0 {
		fmt.Println(env.Dim()("No price history recorded yet. Run hoard update-prices to start."))
		return nil
	}

	cutoff := time.Now().UTC().Add(-window)
	changes, err := st.Movers(cutoff.Format(time.RFC3339))
	if err != nil {
		return err
	}
	// A date, not "over the last 7d". Prices are observed when a refresh runs,
	// not continuously, so what is being compared is today's price against the
	// last one recorded on or before that date — which, on a hoard refreshed
	// every few weeks, may have been recorded well before it. Naming the date
	// says that; naming the window would imply the move happened inside it.
	fmt.Print(report.Movers(env, changes, *limit, "since "+cutoff.Local().Format("2 Jan 2006")))

	// History that does not reach back as far as the window was asked to look is
	// worth saying: the answer is right for the data, and the data is younger
	// than the question.
	if t, err := time.Parse(time.RFC3339, oldest); err == nil && t.After(cutoff) {
		fmt.Println(env.Dim()(fmt.Sprintf(
			"Prices have only been recorded since %s.", t.Local().Format("2 Jan 2006"))))
	}
	return nil
}

// cmdBackfillPrices loads the prices MTGJSON kept while hoard was not looking,
// so a fresh hoard can answer "what moved this month" immediately.
//
// A one-off, and separate from update-prices for a reason: the archive is ~150 MB
// against the 5 MB of today's file, and the download cache is pruned nightly.
//
// Only what is held gets backfilled — reconstructing history for cards nobody owns
// is not worth the wait.
func cmdBackfillPrices(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("backfill-prices", flag.ContinueOnError)
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	owned, err := st.OwnedByFinish()
	if err != nil {
		return err
	}
	if len(owned) == 0 {
		fmt.Println(env.Dim()("Nothing owned yet."))
		return nil
	}
	_, oldest, err := st.PriceHistoryDepth()
	if err != nil {
		return err
	}

	refs := make([]pricing.Ref, len(owned))
	printings := map[string]bool{}
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
		printings[o.ScryfallID] = true
	}

	fmt.Printf("Fetching 90 days of prices for %s printings from MTGJSON (~150 MB)...\n",
		ui.Count(len(printings)))
	byCard, resolvable, err := newFetcher(st).History(ctx, refs)
	if err != nil {
		return err
	}

	inserted, cards, err := st.BackfillPrices(byCard, oldest)
	if err != nil {
		return err
	}

	printBackfill(env, backfillResult{
		printings: len(printings), unmapped: len(printings) - resolvable,
		unquoted: resolvable - len(byCard), inserted: inserted, cards: cards,
		hadHistorySince: oldest,
	})
	return nil
}

// backfillResult is what one import did, and what it could not reach.
type backfillResult struct {
	printings, unmapped, unquoted, inserted, cards int
	hadHistorySince                                string
}

// printBackfill reports the import, including what it missed.
//
// The misses are not filler. Movers joins a card against its own baseline, so a
// printing with no backfilled history simply stops appearing in any window that
// predates hoard — the list quietly gets shorter rather than visibly incomplete.
// Saying how many were skipped is the only place that becomes visible.
func printBackfill(env ui.Env, r backfillResult) {
	dim := env.Dim()
	if r.inserted == 0 {
		if r.hadHistorySince != "" {
			if t, err := time.Parse(time.RFC3339, r.hadHistorySince); err == nil {
				fmt.Println(dim(fmt.Sprintf(
					"Nothing to backfill: prices are already recorded from %s.",
					t.Local().Format("2 Jan 2006"))))
				return
			}
		}
		fmt.Println(dim("MTGJSON had no earlier prices for anything you hold."))
		return
	}

	fmt.Printf("Backfilled %s observations across %s printings.\n",
		ui.Count(r.inserted), ui.Count(r.cards))
	if r.unmapped > 0 {
		fmt.Println(dim(fmt.Sprintf(
			"  %s printings have no MTGJSON id and were skipped.", ui.Count(r.unmapped))))
	}
	if r.unquoted > 0 {
		fmt.Println(dim(fmt.Sprintf(
			"  %s have no TCGplayer price history — the same gap 'unpriced' reports.",
			ui.Count(r.unquoted))))
	}
	fmt.Println(dim("Prices come from TCGplayer, the source Scryfall itself quotes."))
	fmt.Println()
	fmt.Println("Try: hoard movers --since 30d")
}

// parseWindow reads a lookback like 7d, 2w or 48h.
//
// Go's ParseDuration stops at hours, and nobody asks for price movement in
// hours-since-720. Days and weeks are spelled out here and everything else is
// handed to the standard parser.
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("--since needs a value, e.g. 30d")
	}
	unit := time.Duration(0)
	switch s[len(s)-1] {
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	}
	if unit > 0 {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q: want something like 7d, 2w or 48h", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --since %q: want something like 7d, 2w or 48h", s)
	}
	return d, nil
}
