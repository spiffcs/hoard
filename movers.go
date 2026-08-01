package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdMovers(st *store.Store, args []string, jsonOut bool) error {
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
	cutoff := time.Now().UTC().Add(-window)
	m, err := action.Deps{Store: st}.Movers(cutoff.Format(time.RFC3339))
	if err != nil {
		return err
	}
	// An empty result means "nothing moved", which is indistinguishable from
	// "nothing was ever recorded" unless the difference is stated outright —
	// in prose for a reader, by recordedSince's absence for a script.
	if m.Observations == 0 {
		if jsonOut {
			return hoardjson.Write(os.Stdout,
				hoardjson.FromMovers(cutoff.Format(time.RFC3339), "", nil))
		}
		fmt.Println(env.Dim()("No price history recorded yet. Run hoard update-prices to start."))
		return nil
	}
	oldest, changes := m.Oldest, m.Changes
	if jsonOut {
		// The whole answer, not the display's top-N: --limit shapes the
		// tables, and a consumer slices its own.
		return hoardjson.Write(os.Stdout,
			hoardjson.FromMovers(cutoff.Format(time.RFC3339), oldest, changes))
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

// cmdBackfillPrices loads the prices MTGJSON kept while hoard was not
// looking, so a fresh hoard can answer "what moved this month" immediately.
// A one-off, and separate from update-prices for a reason: the archive is
// ~150 MB against the 5 MB of today's file, and the download cache is
// pruned nightly.
func cmdBackfillPrices(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("backfill-prices", flag.ContinueOnError)
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	pr := stderrPrinter()
	res, err := action.BackfillPrices(ctx,
		action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()}, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	if res.Printings == 0 {
		fmt.Println(env.Dim()("Nothing owned yet."))
		return nil
	}
	printBackfill(env, res)
	return nil
}

// printBackfill reports the import, including what it missed.
func printBackfill(env ui.Env, r action.BackfillResult) {
	dim := env.Dim()
	if r.Inserted == 0 {
		if r.HadHistorySince != "" {
			if t, err := time.Parse(time.RFC3339, r.HadHistorySince); err == nil {
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
		ui.Count(r.Inserted), ui.Count(r.Cards))
	if r.Unmapped > 0 {
		fmt.Println(dim(fmt.Sprintf(
			"  %s printings have no MTGJSON id and were skipped.", ui.Count(r.Unmapped))))
	}
	if r.Unquoted > 0 {
		fmt.Println(dim(fmt.Sprintf(
			"  %s have no TCGplayer price history — the same gap 'unpriced' reports.",
			ui.Count(r.Unquoted))))
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
