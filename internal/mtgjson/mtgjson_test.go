package mtgjson

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// gzipped compresses a JSON fixture the way MTGJSON serves its files.
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

// serve stands up a server for the given path→body map and points apiBase at it.
func serve(t *testing.T, files map[string][]byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
}

// priceFile mirrors the real shape: a meta object before data (so the seek has
// something to skip), and vendors that differ in currency and coverage.
const priceFile = `{
 "meta": {"date": "2026-07-28", "version": "5.3.0"},
 "data": {
  "uuid-tcg": {"paper": {
    "tcgplayer":   {"currency": "USD", "retail": {"foil": {"2026-07-28": 11.16}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-07-28": 9.49}}}
  }},
  "uuid-ck": {"paper": {
    "cardmarket":  {"currency": "EUR", "retail": {"foil": {"2026-07-28": 4.28}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-07-28": 0.49},
                                                  "normal": {"2026-07-28": 0.34}}},
    "manapool":    {"currency": "USD", "retail": {"foil": {"2026-07-28": 6.91}}}
  }},
  "uuid-eur-only": {"paper": {
    "cardmarket": {"currency": "EUR", "retail": {"foil": {"2026-07-28": 4.28}}}
  }},
  "uuid-stale": {"paper": {
    "manapool": {"currency": "USD", "retail": {"foil": {"2026-07-01": 1.00,
                                                        "2026-07-28": 2.50}}}
  }},
  "uuid-unwanted": {"paper": {
    "tcgplayer": {"currency": "USD", "retail": {"foil": {"2026-07-28": 999.0}}}
  }},
  "uuid-ripple": {"paper": {
    "tcgplayer":   {"currency": "USD", "retail": {"normal": {"2026-07-28": 0.34}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-07-28": 0.49}}}
  }}
 }
}`

// The exact shape of the MH3 ripple foils: the first-choice vendor prices the
// card, but only in the finish we do not need. Taking its price and stopping
// leaves the foil unpriced while the row looks filled.
func TestTodayPricesResolvesEachFinishSeparately(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, priceFile)})

	got, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	p := got["uuid-ripple"]
	if p.Foil == nil || *p.Foil != 0.49 {
		t.Errorf("foil = %v, want 0.49 from cardkingdom; tcgplayer has no foil price", p.Foil)
	}
	if p.USD == nil || *p.USD != 0.34 {
		t.Errorf("normal = %v, want 0.34 from tcgplayer", p.USD)
	}
	// Each finish records its own vendor, so neither price is credited to a
	// shop that had nothing to do with it.
	if p.USDSource != "tcgplayer" || p.FoilSource != "cardkingdom" {
		t.Errorf("sources = %q/%q, want tcgplayer/cardkingdom", p.USDSource, p.FoilSource)
	}
}

func TestTodayPricesPrefersProvidersInOrder(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, priceFile)})

	want := map[string]bool{"uuid-tcg": true, "uuid-ck": true, "uuid-eur-only": true, "uuid-stale": true}
	got, err := TodayPrices(context.Background(), Options{}, want)
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}

	// TCGplayer wins when present: it is the source Scryfall itself uses.
	if p := got["uuid-tcg"]; p.FoilSource != "tcgplayer" || p.Foil == nil || *p.Foil != 11.16 {
		t.Errorf("uuid-tcg = %+v, want tcgplayer 11.16", p)
	}
	// Falls through to Card Kingdom for both finishes, since TCGplayer quotes
	// neither and Cardmarket is euros.
	p := got["uuid-ck"]
	if p.FoilSource != "cardkingdom" || p.Foil == nil || *p.Foil != 0.49 {
		t.Errorf("uuid-ck = %+v, want cardkingdom foil 0.49", p)
	}
	if p.USD == nil || *p.USD != 0.34 || p.USDSource != "cardkingdom" {
		t.Errorf("uuid-ck normal = %v from %q, want 0.34 from cardkingdom", p.USD, p.USDSource)
	}
	// EUR-only is skipped entirely: a euro price cannot join a USD total.
	if _, ok := got["uuid-eur-only"]; ok {
		t.Error("a EUR-only card must not be priced")
	}
	// Most recent date wins.
	if p := got["uuid-stale"]; p.Foil == nil || *p.Foil != 2.50 {
		t.Errorf("uuid-stale = %+v, want the 2026-07-28 price", p)
	}
	// Cards not asked for are skipped, not returned.
	if _, ok := got["uuid-unwanted"]; ok {
		t.Error("returned a card that was not requested")
	}
	if len(got) != 3 {
		t.Errorf("got %d prices, want 3", len(got))
	}
}

// quoteFile carries what TodayPrices deliberately throws away: every vendor,
// both sides of the counter. Modelled on the real Legion Loyalty record.
const quoteFile = `{
 "meta": {"date": "2026-07-28"},
 "data": {
  "uuid-legion": {"paper": {
    "cardkingdom": {"currency": "USD",
      "retail":  {"normal": {"2026-07-28": 0.99}, "foil": {"2026-07-28": 2.49}},
      "buylist": {"foil": {"2026-07-28": 0.75}}},
    "tcgplayer":  {"currency": "USD", "retail": {"normal": {"2026-07-28": 0.42}}},
    "manapool":   {"currency": "USD",
      "retail": {"normal": {"2026-07-28": 0.20}, "foil": {"2026-07-28": 138518.78}}},
    "cardmarket": {"currency": "EUR",
      "retail": {"normal": {"2026-07-28": 0.25}, "foil": {"2026-07-28": 0.46}}}
  }}
 }
}`

func TestTodayQuotesReturnsEveryVendorAndSide(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, quoteFile)})

	got, err := TodayQuotes(context.Background(), Options{}, map[string]bool{"uuid-legion": true})
	if err != nil {
		t.Fatalf("TodayQuotes: %v", err)
	}
	qs := got["uuid-legion"]

	// Nothing is collapsed: comparing vendors is the point.
	byKey := map[string]float64{}
	for _, q := range qs {
		byKey[q.Provider+"/"+q.Kind+"/"+q.Finish] = q.Price
	}
	for key, want := range map[string]float64{
		"cardkingdom/retail/normal": 0.99,
		"cardkingdom/retail/foil":   2.49,
		"cardkingdom/buylist/foil":  0.75,
		"tcgplayer/retail/normal":   0.42,
		"manapool/retail/normal":    0.20,
		"manapool/retail/foil":      138518.78,
	} {
		if byKey[key] != want {
			t.Errorf("%s = %v, want %v", key, byKey[key], want)
		}
	}
	// Buylist is what TodayPrices could never report.
	if byKey["cardkingdom/buylist/foil"] == 0 {
		t.Error("buylist quotes must be included")
	}
	// Cardmarket quotes euros, which cannot be compared against dollars.
	for _, q := range qs {
		if q.Provider == "cardmarket" {
			t.Errorf("cardmarket must be excluded, got %+v", q)
		}
	}
	if len(qs) != 6 {
		t.Errorf("got %d quotes, want 6: %+v", len(qs), qs)
	}
}

func TestTodayQuotesEmptyRequestSkipsDownload(t *testing.T) {
	old := apiBase
	apiBase = "http://127.0.0.1:1" // would fail instantly if dialled
	defer func() { apiBase = old }()

	got, err := TodayQuotes(context.Background(), Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("TodayQuotes(nil) = %v, %v; want empty with no request", got, err)
	}
}

func TestTodayPricesEmptyRequestSkipsDownload(t *testing.T) {
	// No server at all: an empty request must not make one.
	old := apiBase
	apiBase = "http://127.0.0.1:1" // would fail instantly if dialled
	defer func() { apiBase = old }()

	got, err := TodayPrices(context.Background(), Options{}, nil)
	if err != nil || got != nil {
		t.Errorf("TodayPrices(nil) = %v, %v; want nil, nil with no request", got, err)
	}
}

func TestTodayPricesRejectsTruncatedGzip(t *testing.T) {
	full := gzipped(t, priceFile)
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": full[:len(full)/2]})

	if _, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-tcg": true}); err == nil {
		t.Error("want an error for a truncated download, got nil")
	}
}

const m3cFile = `{
 "meta": {"date": "2026-07-28"},
 "data": {"code": "M3C", "cards": [
   {"uuid": "uuid-ck", "identifiers": {"scryfallId": "scry-1"}},
   {"uuid": "uuid-tcg", "identifiers": {"scryfallId": "scry-2"}},
   {"uuid": "uuid-none", "identifiers": {}}
 ]}
}`

func TestSetIdentifiers(t *testing.T) {
	serve(t, map[string][]byte{"/M3C.json.gz": gzipped(t, m3cFile)})

	// Lower-case set codes come from Scryfall; MTGJSON file names are upper.
	got, err := SetIdentifiers(context.Background(), Options{}, "m3c")
	if err != nil {
		t.Fatalf("SetIdentifiers: %v", err)
	}
	if got["scry-1"] != "uuid-ck" || got["scry-2"] != "uuid-tcg" {
		t.Errorf("map = %v, want both Scryfall IDs mapped", got)
	}
	// A card with no Scryfall ID cannot be joined, so it is dropped.
	if len(got) != 2 {
		t.Errorf("map has %d entries, want 2", len(got))
	}
}

func TestCacheAvoidsRefetchAndPrunesOldDays(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(gzipped(t, priceFile))
	}))
	defer srv.Close()
	oldBase, oldDay := apiBase, today
	apiBase = srv.URL
	cacheDir := t.TempDir()
	today = func() string { return "2026-07-29" }
	defer func() { apiBase, today = oldBase, oldDay }()

	// A leftover from a previous day, which should be swept on the next write.
	stale := filepath.Join(cacheDir, "2026-07-28-AllPricesToday.json.gz")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{"uuid-tcg": true}
	for i := range 3 {
		if _, err := TodayPrices(context.Background(), Options{CacheDir: cacheDir}, want); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// A card no source can price stays a gap forever, so without the cache
	// every run would re-download the whole bundle chasing it.
	if hits != 1 {
		t.Errorf("made %d requests across 3 calls, want 1", hits)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("yesterday's cache entry should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "2026-07-29-AllPricesToday.json.gz")); err != nil {
		t.Errorf("today's entry should be cached: %v", err)
	}
}

func TestSetIdentifiersUnknownSet(t *testing.T) {
	serve(t, map[string][]byte{})

	// Scryfall and MTGJSON disagree on some promo sets. That must be skippable,
	// not fatal, so it gets its own sentinel.
	_, err := SetIdentifiers(context.Background(), Options{}, "nope")
	if !errors.Is(err, ErrNoSuchSet) {
		t.Errorf("err = %v, want ErrNoSuchSet", err)
	}
}

// archiveFileBody is AllPrices' shape: the same nesting as today's file, but
// with a run of dates under each finish rather than one.
const archiveFileBody = `{
 "meta": {"date": "2026-07-29", "version": "5.3.0"},
 "data": {
  "uuid-hist": {"paper": {
    "tcgplayer": {"currency": "USD", "retail": {
        "normal": {"2026-07-27": 1.00, "2026-07-28": 1.25, "2026-07-29": 1.25},
        "foil":   {"2026-07-28": 4.00, "2026-07-29": 4.50}},
      "buylist": {"normal": {"2026-07-28": 0.40}}},
    "cardkingdom": {"currency": "USD", "retail": {
        "normal": {"2026-07-28": 9.99}}}
  }},
  "uuid-eur-tcg": {"paper": {
    "tcgplayer": {"currency": "EUR", "retail": {"normal": {"2026-07-28": 2.00}}}
  }},
  "uuid-no-tcg": {"paper": {
    "manapool": {"currency": "USD", "retail": {"normal": {"2026-07-28": 3.00}}}
  }}
 }
}`

func TestPriceHistoryKeepsEveryDateForTCGplayerRetail(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-hist": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	obs := got["uuid-hist"]
	// Three normal dates and two foil: the whole series, not just the newest.
	if len(obs) != 5 {
		t.Fatalf("got %d observations, want 5: %+v", len(obs), obs)
	}
	for _, o := range obs {
		if o.Source != "tcgplayer" {
			t.Errorf("observation %+v: source = %q, want tcgplayer", o, o.Source)
		}
		// Card Kingdom's 9.99 and the 0.40 buylist are the two figures that must
		// not appear: one is another shop's retail, the other is the wrong side
		// of the counter, and both would read as a price move.
		if o.Price == 9.99 || o.Price == 0.40 {
			t.Errorf("observation %+v leaked from another vendor or the buylist", o)
		}
	}
}

func TestPriceHistorySkipsNonUSDAndAbsentProvider(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	// A EUR tcgplayer series and a card tcgplayer never quoted are both absent
	// rather than converted or substituted: a euro price in a USD total is a
	// lie, and another vendor's series would not join up with Scryfall's.
	got, err := PriceHistory(context.Background(), Options{},
		map[string]bool{"uuid-eur-tcg": true, "uuid-no-tcg": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

func TestPriceHistoryEmptyRequestSkipsDownload(t *testing.T) {
	old := apiBase
	apiBase = "http://127.0.0.1:1" // would fail instantly if dialled
	defer func() { apiBase = old }()

	// Nothing owned must not pull 150 MB to discover that.
	got, err := PriceHistory(context.Background(), Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty and no error", got, err)
	}
}

// A cache directory that cannot be created must degrade to an uncached
// download. The close of the response body used to be deferred across this
// fallback, handing the caller a body that was already closed.
func TestUnusableCacheDirStillServes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(gzipped(t, priceFile))
	}))
	defer srv.Close()
	oldBase := apiBase
	apiBase = srv.URL
	defer func() { apiBase = oldBase }()

	// MkdirAll fails because the parent is a regular file.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "cache")

	got, err := TodayPrices(context.Background(), Options{CacheDir: dir}, map[string]bool{"uuid-tcg": true})
	if err != nil {
		t.Fatalf("TodayPrices with an unusable cache dir: %v", err)
	}
	if len(got) == 0 {
		t.Error("no prices served through the uncached fallback")
	}
}
