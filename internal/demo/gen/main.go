// Command gen writes the price history `hoard demo` seeds.
//
// Build machinery, not a hoard command: it lives under internal/ so
// `go install ./...` never puts it on anyone's PATH, and it reaches the
// network, which nothing in the released binary's demo path does.
//
//	go run ./internal/demo/gen internal/demo/history.json
//
// It seeds a throwaway database from internal/demo/collection.json, runs
// the real backfill against MTGJSON, and writes back what actually landed in
// the two history tables. Deriving it from a live backfill rather than
// hand-assembling a series is the same argument the sample collection makes
// for going through the merge path: the file cannot describe a shape hoard
// would not itself record, because hoard recorded it.
//
// Re-run it whenever collection.json is regenerated. The two are a pair — a
// history file naming printings the collection no longer holds seeds nothing,
// silently — and both freeze at the moment they are made.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/demo"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

// backfillDays is the archive's full reach. The demo takes all of it: the
// movers view cycles a 7-, 30- and 90-day window, and the deepest one is the
// only reason a sample collection has anything to show in the other two once
// the file has aged.
const backfillDays = 90

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <output.json>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "demo/gen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	dir, err := os.MkdirTemp("", "hoard-demo-gen")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	st, err := store.Open(filepath.Join(dir, "demo.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	seed, err := action.SeedHoard(st, demo.Collection, "the sample collection")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "seeded %d printings, %d copies\n", seed.Printings, seed.Copies)

	// The real cache, so a re-run the same day costs a file read rather than
	// another ~150 MB download.
	deps := action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir()}
	p := progress.Fn(func(ev progress.Event) {
		if ev.Note != "" {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", ev.Step, ev.Note)
		}
	})
	res, err := action.BackfillPrices(context.Background(), deps, p, backfillDays)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "backfilled %d observations across %d printings, %d bids\n",
		res.Inserted, res.Cards, res.BidInserted)
	if res.Inserted == 0 {
		return fmt.Errorf("the backfill recorded nothing; refusing to write an empty history")
	}

	doc := demo.HistoryDocument{
		Kind:        demo.HistoryKind,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	ids, err := st.ActivePrintingIDs()
	if err != nil {
		return err
	}
	// Every finish with rows, not only the held one. The card detail loads both
	// finishes on purpose — "a card owned in non-foil is often being looked at
	// precisely because its foil is doing something" — so a document trimmed to
	// holdings would give the demo a sparkline the real thing draws and this
	// one cannot. The backfill records a finish the printing is sold in whether
	// or not anyone holds it, so these rows cost nothing to have collected.
	for _, id := range ids {
		for _, finish := range []string{"nonfoil", "foil", "etched"} {
			retail, err := series(id, finish, st.PriceSeries)
			if err != nil {
				return err
			}
			doc.Retail = appendSeries(doc.Retail, retail)

			bids, err := series(id, finish, st.BidSeries)
			if err != nil {
				return err
			}
			doc.Bids = appendSeries(doc.Bids, bids)
		}
	}

	b, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d retail series, %d bid series, %d KB\n",
		out, len(doc.Retail), len(doc.Bids), len(b)>>10)
	return nil
}

// series reads one holding's stored observations back into document form.
//
// A series carries one source, and hoard's archive reader guarantees it: it
// picks a single vendor per card and finish and takes that vendor's series
// whole. A mixed one here would mean the guarantee broke, so it is an error
// rather than a shrug — writing it out would bake a spliced series into every
// demo, and a splice reads as price movement that never happened.
func series(id, finish string, read func(string, string) ([]store.PricePoint, error)) (demo.HistorySeries, error) {
	points, err := read(id, finish)
	if err != nil {
		return demo.HistorySeries{}, err
	}
	s := demo.HistorySeries{ScryfallID: id, Finish: finish}
	for _, p := range points {
		if s.Source == "" {
			s.Source = p.Source
		} else if p.Source != s.Source {
			return s, fmt.Errorf("%s %s mixes sources %q and %q", id, finish, s.Source, p.Source)
		}
		date, ok := cutDate(p.AsOf)
		if !ok {
			return s, fmt.Errorf("%s %s has an unreadable timestamp %q", id, finish, p.AsOf)
		}
		s.Points = append(s.Points, demo.HistoryPoint{Date: date, Price: p.Price})
	}
	return s, nil
}

// appendSeries drops the empty ones: a card with no bid history is absent from
// the document, not present with nothing in it.
func appendSeries(dst []demo.HistorySeries, s demo.HistorySeries) []demo.HistorySeries {
	if len(s.Points) == 0 {
		return dst
	}
	return append(dst, s)
}

// cutDate takes the date off an RFC3339 stamp. The store keeps observations at
// full precision; the archive's own granularity is the day, and every row this
// reads back was written from one.
func cutDate(asOf string) (date string, ok bool) {
	if len(asOf) < 10 || asOf[4] != '-' || asOf[7] != '-' {
		return "", false
	}
	return asOf[:10], true
}
