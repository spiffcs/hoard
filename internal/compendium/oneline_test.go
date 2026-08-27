package compendium

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func manyCards(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, `{"object":"card","id":"scry-%d","name":"Card %d","set":"mh2",`+
			`"set_name":"Modern Horizons 2","collector_number":"%d","released_at":"2021-06-18",`+
			`"rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],`+
			`"scryfall_uri":"https://scryfall.com/card/mh2/%d","prices":{"usd":"1.00"}}`+"\n",
			i, i, i, i)
	}
	return b.String()
}

func TestSeedingReportsOneStepForTheWholeDownload(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cid, err := st.CollectionID()
	if err != nil {
		t.Fatalf("CollectionID: %v", err)
	}

	o := Options{BulkListingURL: serveScryfall(t, manyCards(6000)), CacheDir: t.TempDir()}
	f, err := newFilter(o)
	if err != nil {
		t.Fatalf("newFilter: %v", err)
	}

	var events []progress.Event
	printings, _, err := seedPrintings(context.Background(), st, cid, o, f,
		func(ev progress.Event) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("seedPrintings: %v", err)
	}
	if printings != 6000 {
		t.Fatalf("seeded %d printings, want 6000", printings)
	}

	if len(events) < 3 {
		t.Fatalf("%d progress events, want a stream of them", len(events))
	}
	for i, ev := range events {
		if ev.Step != "downloading catalog" {
			t.Fatalf("event %d has step %q, want every event on the one step "+
				"%q so the bar stays on one line", i, ev.Step, "downloading catalog")
		}
	}

	details := map[string]bool{}
	for _, ev := range events {
		details[ev.Detail] = true
	}
	if len(details) < 2 {
		t.Errorf("details %v never advanced — the card tally is not being reported", details)
	}
	if got := events[len(events)-1].Detail; got != "6,000 cards" {
		t.Errorf("last detail = %q, want %q", got, "6,000 cards")
	}
}

func rowsUsed(out string) int {
	n := strings.Count(out, "\n")
	for _, m := range regexp.MustCompile(`\x1b\[(\d+)A`).FindAllStringSubmatch(out, -1) {
		up, _ := strconv.Atoi(m[1])
		n -= up
	}
	return n
}

func TestAFullBuildFitsInOneWindow(t *testing.T) {
	prices, _, _ := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var sb strings.Builder
	pr := ui.NewPrinterSize(&sb, true, 100, 40)

	if _, err := Build(context.Background(), st, Options{
		Days:           30,
		BulkListingURL: serveScryfall(t, manyCards(6000)),
		PriceBaseURL:   serveMTGJSON(t, prices),
		TCGCSVBaseURL:  offlineTCGCSV,
		CacheDir:       t.TempDir(),
	}, pr.Fn()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	pr.Close()

	if got := rowsUsed(sb.String()); got > 7 {
		t.Errorf("a full build used %d terminal rows, want at most 7 — "+
			"the whole run must stay in one window", got)
	}
}
