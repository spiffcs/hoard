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
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

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

	for _, id := range ids {
		for _, fin := range finish.All() {
			retail, err := series(id, fin, st.PriceSeries)
			if err != nil {
				return err
			}
			doc.Retail = appendSeries(doc.Retail, retail)

			bids, err := series(id, fin, st.BidSeries)
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

func series(id string, fin finish.Finish, read func(string, finish.Finish) ([]store.PricePoint, error)) (demo.HistorySeries, error) {
	points, err := read(id, fin)
	if err != nil {
		return demo.HistorySeries{}, err
	}
	s := demo.HistorySeries{ScryfallID: id, Finish: fin}
	for _, p := range points {
		if s.Source == "" {
			s.Source = p.Source
		} else if p.Source != s.Source {
			return s, fmt.Errorf("%s %s mixes sources %q and %q", id, fin, s.Source, p.Source)
		}
		date, ok := cutDate(p.AsOf)
		if !ok {
			return s, fmt.Errorf("%s %s has an unreadable timestamp %q", id, fin, p.AsOf)
		}
		s.Points = append(s.Points, demo.HistoryPoint{Date: date, Price: p.Price})
	}
	return s, nil
}

func appendSeries(dst []demo.HistorySeries, s demo.HistorySeries) []demo.HistorySeries {
	if len(s.Points) == 0 {
		return dst
	}
	return append(dst, s)
}

func cutDate(asOf string) (date string, ok bool) {
	if len(asOf) < 10 || asOf[4] != '-' || asOf[7] != '-' {
		return "", false
	}
	return asOf[:10], true
}
