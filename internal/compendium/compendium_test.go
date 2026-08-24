package compendium

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

func gzipped(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

const bulkJSONL = `{"object":"card","id":"scry-1","name":"Ragavan, Nimble Pilferer","set":"mh2","set_name":"Modern Horizons 2","collector_number":"138","released_at":"2021-06-18","rarity":"mythic","lang":"en","games":["paper","mtgo"],"finishes":["nonfoil","foil"],"type_line":"Legendary Creature — Monkey Pirate","oracle_text":"Dash {1}{R}","scryfall_uri":"https://scryfall.com/card/mh2/138","prices":{"usd":"60.00","usd_foil":"75.00"}}
{"object":"card","id":"scry-2","name":"Sol Ring","set":"c21","set_name":"Commander 2021","collector_number":"1","released_at":"2021-04-23","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"type_line":"Artifact","oracle_text":"{T}: Add {C}{C}.","scryfall_uri":"https://scryfall.com/card/c21/1","prices":{"usd":"2.00"}}
{"object":"card","id":"scry-digital","name":"Arena Only","set":"ana","set_name":"Arena New Player Experience","collector_number":"1","released_at":"2021-01-01","rarity":"common","lang":"en","games":["arena"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/ana/1","prices":{"usd":"1.00"}}
`

const allPrintings = `{
 "meta": {"date": "2026-08-22"},
 "data": {
  "MH2": {"code": "MH2", "cards": [
    {"uuid": "uuid-ragavan", "identifiers": {"scryfallId": "scry-1"}}
  ]},
  "C21": {"code": "C21", "cards": [
    {"uuid": "uuid-solring", "identifiers": {"scryfallId": "scry-2"}}
  ]}
 }
}`

func allPrices(t *testing.T) (body string, oldest, newest string) {
	t.Helper()
	ragavan := map[string]float64{}
	solring := map[string]float64{}
	now := time.Now()
	for i := 39; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		ragavan[d] = 60.0 + float64(i)
		solring[d] = 2.0 + float64(i)/100
	}
	oldest = now.AddDate(0, 0, -39).Format("2006-01-02")
	newest = now.Format("2006-01-02")

	rag, _ := json.Marshal(ragavan)
	sol, _ := json.Marshal(solring)
	return fmt.Sprintf(`{"meta":{"date":%q},"data":{
  "uuid-ragavan":{"paper":{"tcgplayer":{"currency":"USD","retail":{"normal":%s}}}},
  "uuid-solring":{"paper":{"tcgplayer":{"currency":"USD","retail":{"normal":%s}}}}
 }}`, newest, rag, sol), oldest, newest
}

func serveScryfall(t *testing.T, jsonl string) string {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/default-cards.jsonl.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(gzipped(t, jsonl))
	})
	mux.HandleFunc("/bulk-data", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"type":"default_cards","updated_at":"2026-08-22T21:05:38.265+00:00",
		  "jsonl_download_uri":%q,"compressed_size":%d}]}`,
			base+"/default-cards.jsonl.gz", len(jsonl))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv.URL + "/bulk-data"
}

func serveMTGJSON(t *testing.T, prices string) string {
	t.Helper()
	files := map[string][]byte{
		"/AllPrintings.json.gz": gzipped(t, allPrintings),
		"/AllPrices.json.gz":    gzipped(t, prices),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBuildsABrowsableCatalog(t *testing.T) {
	prices, oldest, newest := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	res, err := Build(context.Background(), st, Options{
		Days:           30,
		BulkListingURL: serveScryfall(t, bulkJSONL),
		PriceBaseURL:   serveMTGJSON(t, prices),
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(progress.Event) {}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if res.Printings != 2 {
		t.Errorf("seeded %d printings, want 2 — the arena-only card must be skipped", res.Printings)
	}
	if res.Entries != 3 {
		t.Errorf("seeded %d entries, want 3 (Ragavan in two finishes, Sol Ring in one)", res.Entries)
	}
	if res.Mapped != 2 {
		t.Errorf("mapped %d mtgjson ids, want 2", res.Mapped)
	}

	series, err := st.PriceSeries("scry-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) < 25 {
		t.Fatalf("PriceSeries returned %d points, want a full 30-day window — "+
			"the detail view has nothing to chart", len(series))
	}

	cutoff := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	for i, p := range series {
		if _, err := time.Parse(time.RFC3339, p.AsOf); err != nil {
			t.Fatalf("point %d has AsOf %q, which ui.Resample cannot parse: %v", i, p.AsOf, err)
		}
		if p.AsOf[:10] < cutoff {
			t.Errorf("point %d is dated %s, older than the %d-day cutoff %s",
				i, p.AsOf[:10], 30, cutoff)
		}
		if i > 0 && series[i-1].AsOf >= p.AsOf {
			t.Errorf("series is not ascending: %s then %s", series[i-1].AsOf, p.AsOf)
		}
	}
	if got := series[len(series)-1].AsOf[:10]; got != newest {
		t.Errorf("newest point is %s, want today (%s)", got, newest)
	}
	if got := series[0].AsOf[:10]; got == oldest {
		t.Errorf("oldest point is %s — the 40-day archive was not clipped to 30 days", got)
	}
	if src := series[0].Source; !strings.Contains(src, "tcgplayer") {
		t.Errorf("source = %q, want tcgplayer", src)
	}

	sets, err := st.SetsHeld()
	if err != nil {
		t.Fatalf("SetsHeld: %v", err)
	}
	if len(sets) != 2 {
		t.Errorf("SetsHeld = %+v, want the two paper sets in the left pane", sets)
	}
}

func TestBuildStampsTheDatabaseAsACatalog(t *testing.T) {
	prices, _, _ := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if _, err := Build(context.Background(), st, Options{
		Days:           30,
		BulkListingURL: serveScryfall(t, bulkJSONL),
		PriceBaseURL:   serveMTGJSON(t, prices),
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(progress.Event) {})); err != nil {
		t.Fatalf("Build: %v", err)
	}
}
