package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// defaultMoverRows is how many risers and sinkers a section shows before it is
// truncated. Ten fits a terminal beside the rest of a refresh's output; the
// tail is what --limit is for.
const defaultMoverRows = 10

func cmdMovers(st *store.Store, args []string) error {
	fs := flag.NewFlagSet("movers", flag.ContinueOnError)
	since := fs.String("since", "30d", "how far back to compare (e.g. 7d, 2w, 48h)")
	limit := fs.Int("limit", defaultMoverRows, "rows per section")
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
	printMovers(env, changes, *limit, "since "+cutoff.Local().Format("2 Jan 2006"))

	// History that does not reach back as far as the window was asked to look is
	// worth saying: the answer is right for the data, and the data is younger
	// than the question.
	if t, err := time.Parse(time.RFC3339, oldest); err == nil && t.After(cutoff) {
		fmt.Println(env.Dim()(fmt.Sprintf(
			"Prices have only been recorded since %s.", t.Local().Format("2 Jan 2006"))))
	}
	return nil
}

// cmdBackfillPrices loads the prices MTGJSON kept while hoard was not looking.
//
// Price history only starts when the table that holds it does, so a fresh hoard
// answers "what moved in the last month" with a day of data and a footer
// apologising for it. MTGJSON publishes the last ninety days per card, which is
// enough to make the question answerable immediately instead of in March.
//
// This is a one-off by design, and separate from update-prices for that reason:
// the archive is ~150 MB against the 5 MB of today's file, and the download
// cache is pruned nightly, so folding it into the routine refresh would risk
// re-fetching the whole thing on any day someone updates prices.
//
// Only what is held gets backfilled. History for the rest of the catalog is
// worth keeping once observed (a card can leave the collection and come back),
// but it is not worth 150 MB and a wait to reconstruct for cards nobody owns.
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

	need := make([]cardRef, 0, len(owned))
	for _, o := range owned {
		if o.MTGJSONUUID == "" {
			need = append(need, cardRef{ScryfallID: o.ScryfallID, SetCode: o.SetCode})
		}
	}
	uuids, err := resolveMTGJSONIDs(ctx, st, need)
	if err != nil {
		return err
	}

	// Owned rows are per finish, so the same printing appears twice; the import
	// is per printing.
	printings := map[string]bool{}
	want := map[string]bool{}
	sidByUUID := map[string]string{}
	for _, o := range owned {
		printings[o.ScryfallID] = true
		if u := uuidFor(o, uuids); u != "" {
			want[u] = true
			sidByUUID[u] = o.ScryfallID
		}
	}

	fmt.Printf("Fetching 90 days of prices for %s printings from MTGJSON (~150 MB)...\n",
		ui.Count(len(printings)))
	mtgjson.CacheDir = priceCacheDir()
	hist, err := mtgjson.PriceHistory(ctx, want)
	if err != nil {
		return fmt.Errorf("mtgjson price history: %w", err)
	}

	byCard := make(map[string][]mtgjson.Observation, len(hist))
	for uuid, obs := range hist {
		byCard[sidByUUID[uuid]] = obs
	}
	inserted, cards, err := st.BackfillPrices(byCard, oldest)
	if err != nil {
		return err
	}

	printBackfill(env, backfillResult{
		printings: len(printings), unmapped: len(printings) - len(want),
		unquoted: len(want) - len(hist), inserted: inserted, cards: cards,
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

// moverSection is one titled group of rows: the risers, or the sinkers.
type moverSection struct {
	Title string
	Rows  []store.PriceChange
}

// moverSections splits changes into the biggest risers and the biggest sinkers.
//
// Both are ordered by what the move is worth across every copy held rather than
// by the per-copy price change: fifty commons that each gained a dime moved the
// hoard more than one mythic that gained a dollar, and sorting on the sticker
// price buries that.
func moverSections(changes []store.PriceChange, limit int) []moverSection {
	if limit <= 0 {
		limit = defaultMoverRows
	}
	return []moverSection{
		{"RISERS", topMovers(changes, limit,
			func(c store.PriceChange) bool { return c.TotalDelta() > 0 },
			func(a, b store.PriceChange) int { return cmp.Compare(b.TotalDelta(), a.TotalDelta()) })},
		{"SINKERS", topMovers(changes, limit,
			func(c store.PriceChange) bool { return c.TotalDelta() < 0 },
			func(a, b store.PriceChange) int { return cmp.Compare(a.TotalDelta(), b.TotalDelta()) })},
	}
}

// moversTable lays out both sections: what the card is, what it cost, what it
// costs now, and what that did to the hoard.
//
// Risers and sinkers share one table rather than getting one each so that they
// share a column layout. Laid out separately they disagree — a five-figure
// sinker widens its price columns, a narrow terminal then drops different
// columns from each half, and two tables that describe the same thing print
// with different shapes directly above one another.
//
// Columns are given up in order of how little they add — the printing, then the
// finish, then the arrow, then the old price and the percentage — so that what
// survives the narrowest terminal is the card, what it costs now, and what the
// move did to the hoard. The arrow goes before the price it points away from,
// which is what keeps a dangling "→" out of a squeezed row.
func moversTable(env ui.Env, sections []moverSection) ui.Table {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 6, Style: env.Dim()},
			{Title: "WAS", Align: ui.Right, Priority: 3, Style: env.Dim()},
			{Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "NOW", Align: ui.Right},
			{Title: "CHANGE", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right, Priority: 1, Style: env.Dim()},
			{Title: "IMPACT", Align: ui.Right},
		},
	}

	first := true
	for _, sec := range sections {
		if len(sec.Rows) == 0 {
			continue
		}
		if !first {
			t.AddSpacer()
		}
		first = false
		t.AddStyled(env.Bold(), ui.C(sec.Title))
		for _, c := range sec.Rows {
			// A finish column reading "normal" down every row is noise; the
			// foils are what want pointing out.
			finish := c.Finish
			if finish == "normal" {
				finish = "-"
			}
			// The indent lives in the name cell, so every column to its right
			// stays aligned with the section heading above.
			t.Add(ui.C("  "+c.Name), ui.C(c.SetCode+"/"+c.CollectorNumber), ui.C(finish),
				ui.C(ui.Money(c.Old)), ui.C("→"), ui.C(ui.Money(c.New)),
				ui.C(signedPercent(c.Pct())), ui.C("×"+ui.Count(c.Copies)),
				ui.C(signedMoney(c.TotalDelta())))
		}
	}
	return t
}

// printMovers writes the risers and sinkers, and the net effect on the hoard.
func printMovers(env ui.Env, changes []store.PriceChange, limit int, window string) {
	if len(changes) == 0 {
		fmt.Println(env.Dim()("No price changes " + window + "."))
		return
	}

	var net float64
	for _, c := range changes {
		net += c.TotalDelta()
	}

	if _, err := moversTable(env, moverSections(changes, limit)).WriteTo(os.Stdout); err != nil {
		return
	}
	fmt.Println()
	fmt.Println(env.Dim()(fmt.Sprintf("%s printings moved %s. Net change: %s",
		ui.Count(len(changes)), window, signedMoney(net))))
}

// topMovers filters, sorts and truncates one section's rows.
func topMovers(all []store.PriceChange, limit int, keep func(store.PriceChange) bool,
	order func(a, b store.PriceChange) int) []store.PriceChange {
	var out []store.PriceChange
	for _, c := range all {
		if keep(c) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, order)
	return out[:min(len(out), limit)]
}

// signedMoney formats a movement, always carrying its sign. ui.Money already
// writes a minus; only the rise needs marking, so a column of them reads as
// direction rather than as a column of amounts.
func signedMoney(v float64) string {
	if v > 0 {
		return "+" + ui.Money(v)
	}
	return ui.Money(v)
}

// signedPercent formats a movement as a percentage.
//
// ui.Percent is for shares of a total and renders anything at or below zero as
// empty, which is exactly the half of this list that matters. An empty string is
// returned only when the old price was zero, where a percentage is meaningless.
func signedPercent(frac float64) string {
	if frac == 0 {
		return ""
	}
	s := strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
	if frac > 0 {
		return "+" + s
	}
	return s
}
