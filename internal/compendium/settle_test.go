package compendium

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

const settleJSONL = `{"object":"card","id":"scry-1","name":"Ragavan, Nimble Pilferer","layout":"normal","set":"mh2","set_name":"Modern Horizons 2","collector_number":"138","released_at":"2021-06-18","rarity":"mythic","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mh2/138","prices":{"usd":"60.00"}}
{"object":"card","id":"scry-2","name":"Sol Ring","layout":"normal","set":"c21","set_name":"Commander 2021","collector_number":"1","released_at":"2021-04-23","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/c21/1","prices":{"usd":"2.00"}}
{"object":"card","id":"scry-unmapped","name":"Chalice of the Void","layout":"normal","set":"mrd","set_name":"Mirrodin","collector_number":"44","released_at":"2003-10-02","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mrd/44","prices":{"usd":"7.50"}}
`

func stalePrices(t *testing.T) string {
	t.Helper()
	ragavan := map[string]float64{}
	solring := map[string]float64{}
	now := time.Now().UTC()
	for i := 30; i >= 1; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		ragavan[d] = 100.0 + float64(i)
		solring[d] = 9.0 + float64(i)
	}
	rag, _ := json.Marshal(ragavan)
	sol, _ := json.Marshal(solring)
	return fmt.Sprintf(`{"meta":{"date":%q},"data":{
  "uuid-ragavan":{"paper":{"tcgplayer":{"currency":"USD","retail":{"normal":%s}}}},
  "uuid-solring":{"paper":{"tcgplayer":{"currency":"USD","retail":{"normal":%s}}}}
 }}`, now.AddDate(0, 0, -1).Format("2006-01-02"), rag, sol)
}

type settled struct {
	store  *store.Store
	result Result
	events []progress.Event
	days   []string
}

func (s settled) recordedOnBuildDay(t *testing.T, what, asOf string) {
	t.Helper()
	if len(asOf) < 10 {
		t.Fatalf("%s has as-of %q, which carries no date", what, asOf)
	}
	if !slices.Contains(s.days, asOf[:10]) {
		t.Errorf("%s is dated %s, want the day the build ran (%s) — the build left prices "+
			"for the user to fetch themselves", what, asOf[:10], strings.Join(s.days, " or "))
	}
}

func buildSettled(t *testing.T) settled {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var events []progress.Event
	opened := time.Now().UTC().Format("2006-01-02")
	res, err := Build(context.Background(), st, Options{
		Days:           30,
		BulkListingURL: serveScryfall(t, settleJSONL),
		PriceBaseURL:   serveMTGJSON(t, stalePrices(t)),
		TCGCSVBaseURL:  offlineTCGCSV,
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(e progress.Event) { events = append(events, e) }))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	days := []string{opened}
	if closed := time.Now().UTC().Format("2006-01-02"); closed != opened {
		days = append(days, closed)
	}
	return settled{store: st, result: res, events: events, days: days}
}

func TestBuildRecordsTodaysPriceOnTopOfTheArchive(t *testing.T) {
	b := buildSettled(t)

	series, err := b.store.PriceSeries("scry-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) < 2 {
		t.Fatalf("PriceSeries returned %d points, want the archive plus today's recording", len(series))
	}
	if got := series[0].Source; !strings.Contains(got, "tcgplayer") {
		t.Errorf("oldest point source = %q, want the MTGJSON archive still backfilled", got)
	}

	last := series[len(series)-1]
	b.recordedOnBuildDay(t, "the newest point", last.AsOf)
	if last.Source != "scryfall" {
		t.Errorf("newest point source = %q, want scryfall (the price the build just seeded)",
			last.Source)
	}
	if last.Price != 60.00 {
		t.Errorf("newest point price = %v, want the seeded 60.00", last.Price)
	}
}

func TestBuildRecordsPricesTheArchiveNeverCarried(t *testing.T) {
	b := buildSettled(t)

	series, err := b.store.PriceSeries("scry-unmapped", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("PriceSeries returned %d points, want exactly one — MTGJSON has no history "+
			"for this printing, so only the build's own recording can price it", len(series))
	}
	b.recordedOnBuildDay(t, "the only point", series[0].AsOf)
	if series[0].Price != 7.50 {
		t.Errorf("price = %v, want the seeded 7.50", series[0].Price)
	}
}

func TestBuildLeavesNothingForTheFirstUpdatePricesRun(t *testing.T) {
	b := buildSettled(t)

	changes, err := b.store.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("a fresh update-prices run still found %d changes to record, so the build "+
			"handed the user work it should have done: %+v", len(changes), changes)
	}
}

func TestBuildValuesTheCompendiumOnce(t *testing.T) {
	b := buildSettled(t)

	points, err := b.store.ValueSnapshots()
	if err != nil {
		t.Fatalf("ValueSnapshots: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("no value snapshot recorded; the total-value chart opens empty")
	}
	if got := points[len(points)-1].Total; got != 69.50 {
		t.Errorf("newest snapshot total = %v, want 69.50 (60.00 + 2.00 + 7.50)", got)
	}
}

func TestBuildReportsTheSettledPricesInProgressAndResult(t *testing.T) {
	b := buildSettled(t)

	if b.result.Priced != b.result.Printings {
		t.Errorf("Result.Priced = %d, want every one of the %d seeded printings",
			b.result.Priced, b.result.Printings)
	}

	var steps []string
	for _, e := range b.events {
		if e.Step != "" && !slices.Contains(steps, e.Step) {
			steps = append(steps, e.Step)
		}
	}
	if !slices.Contains(steps, "settling prices") {
		t.Errorf("progress steps were %v, want the settling shown to the user as the build does it",
			steps)
	}
	var settling bool
	for _, e := range b.events {
		if e.Step == "settling prices" {
			settling = true
			continue
		}
		if settling && e.Step != "" {
			t.Fatalf("step %q was reported after settling began; the settling must stay on "+
				"one line or the build outgrows its window", e.Step)
		}
	}
}
