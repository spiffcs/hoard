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
	"time"
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
	// TCGplayer quotes neither finish and Cardmarket is euros: the normal
	// falls to Card Kingdom (next in the normal order), while the foil
	// falls to Manapool — the foil order runs marketplace-first.
	p := got["uuid-ck"]
	if p.FoilSource != "manapool" || p.Foil == nil || *p.Foil != 6.91 {
		t.Errorf("uuid-ck = %+v, want manapool foil 6.91", p)
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
   {"uuid": "uuid-ck", "identifiers": {"scryfallId": "scry-1",
    "tcgplayerAlternativeFoilProductId": "553005"},
    "purchaseUrls": {"cardKingdom": "https://mtgjson.com/links/aa",
                     "cardKingdomFoil": "https://mtgjson.com/links/bb"}},
   {"uuid": "uuid-tcg", "identifiers": {"scryfallId": "scry-2",
    "tcgplayerEtchedProductId": "600100"}},
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
	if got["scry-1"].UUID != "uuid-ck" || got["scry-2"].UUID != "uuid-tcg" {
		t.Errorf("map = %v, want both Scryfall IDs mapped", got)
	}
	// The Card Kingdom links ride the same read, per finish; a card the
	// feed has no links for reads as empty, not absent.
	if got["scry-1"].CKURL != "https://mtgjson.com/links/aa" ||
		got["scry-1"].CKFoilURL != "https://mtgjson.com/links/bb" {
		t.Errorf("links = %+v, want both finishes", got["scry-1"])
	}
	if got["scry-2"].CKURL != "" || got["scry-2"].CKFoilURL != "" {
		t.Errorf("linkless card = %+v, want empty links", got["scry-2"])
	}
	// A card with no Scryfall ID cannot be joined, so it is dropped.
	if len(got) != 2 {
		t.Errorf("map has %d entries, want 2", len(got))
	}
	// The split-product ids ride the same read, and stay apart: the
	// alternative-foil id and the etched id name different products with
	// different prices. AltProductID once fell back to the etched id, which
	// let an etched product's price be merged into the foil series.
	if got["scry-1"].AltProductID != "553005" {
		t.Errorf("alt product = %q, want the ripple product id", got["scry-1"].AltProductID)
	}
	if got["scry-2"].AltProductID != "" {
		t.Errorf("alt product = %q, want empty: this card has no treated foil, only an etched product",
			got["scry-2"].AltProductID)
	}
	if got["scry-2"].EtchedProductID != "600100" {
		t.Errorf("etched product = %q, want the etched id in its own field",
			got["scry-2"].EtchedProductID)
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
        "normal": {"2026-07-28": 9.99}},
      "buylist": {
        "normal": {"2026-07-28": 0.75, "2026-07-29": 0.80},
        "foil":   {"2026-07-29": 2.00}}}
  }},
  "uuid-eur-tcg": {"paper": {
    "tcgplayer": {"currency": "EUR", "retail": {"normal": {"2026-07-28": 2.00}}}
  }},
  "uuid-no-tcg": {"paper": {
    "manapool": {"currency": "USD", "retail": {"normal": {"2026-07-28": 3.00}}}
  }},
  "uuid-bids-only": {"paper": {
    "cardkingdom": {"currency": "USD", "buylist": {"normal": {"2026-07-29": 0.10}}}
  }},
  "uuid-ripple": {"paper": {
    "tcgplayer": {"currency": "USD", "retail": {"normal": {"2026-07-28": 11.86}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-07-28": 74.99, "2026-07-29": 75.99}}}
  }}
 }
}`

func TestPriceHistoryKeepsEveryDateForTCGplayerRetail(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-hist": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	obs := got["uuid-hist"].Retail
	// Three normal dates and two foil: the whole series, not just the newest.
	if len(obs) != 5 {
		t.Fatalf("got %d observations, want 5: %+v", len(obs), obs)
	}
	for _, o := range obs {
		if o.Source != "tcgplayer" {
			t.Errorf("observation %+v: source = %q, want tcgplayer", o, o.Source)
		}
		// Card Kingdom's 9.99 retail and every buylist figure must not appear
		// here: one is another shop's retail, the others are the wrong side of
		// the counter, and all would read as a price move.
		if o.Price == 9.99 || o.Price == 0.40 || o.Price == 0.75 || o.Price == 0.80 || o.Price == 2.00 {
			t.Errorf("observation %+v leaked from another vendor or the buylist", o)
		}
	}
}

// The same pass reads the other side of the counter: Card Kingdom's bid
// series, and only Card Kingdom's — tcgplayer's buylist key is dead data
// from before that program closed, and reading it would resurrect it.
func TestPriceHistoryReadsCardKingdomBids(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-hist": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	bids := got["uuid-hist"].Bids
	if len(bids) != 3 {
		t.Fatalf("got %d bids, want the two normal days and one foil: %+v", len(bids), bids)
	}
	for _, o := range bids {
		if o.Source != "cardkingdom" {
			t.Errorf("bid %+v: source = %q, want cardkingdom", o, o.Source)
		}
		if o.Price == 0.40 || o.Price == 9.99 {
			t.Errorf("bid %+v leaked from tcgplayer's buylist or CK's retail", o)
		}
	}
}

// A card tcgplayer never quoted can still carry a bid series — the two
// sides are independent reads, not a join.
func TestPriceHistoryReturnsBidsOnlyCards(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-bids-only": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	h := got["uuid-bids-only"]
	if len(h.Retail) != 0 || len(h.Bids) != 1 || h.Bids[0].Price != 0.10 {
		t.Errorf("got %+v, want no retail and the one bid", h)
	}
}

func TestPriceHistorySkipsNonUSDAndFallsThroughProviders(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	// A EUR tcgplayer series is absent rather than converted — a euro price
	// in a USD total is a lie, and no other vendor quotes the card. But a
	// card TCGplayer never carried takes the next vendor's series: that
	// vendor is also the one pricing it live, so the history joins up.
	got, err := PriceHistory(context.Background(), Options{},
		map[string]bool{"uuid-eur-tcg": true, "uuid-no-tcg": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	if _, ok := got["uuid-eur-tcg"]; ok {
		t.Errorf("EUR-only card must stay absent: %+v", got["uuid-eur-tcg"])
	}
	h := got["uuid-no-tcg"]
	if len(h.Retail) != 1 || h.Retail[0].Price != 3.00 || h.Retail[0].Source != "manapool" {
		t.Errorf("no-tcg card = %+v, want manapool's series", h.Retail)
	}
}

// The ripple-foil shape, observed live: TCGplayer publishes no foil series
// for the MH3 Collector's Edition printings, so the foil history falls to
// the next vendor per finish — the normal series stays TCGplayer's.
func TestPriceHistoryFoilFallsToNextVendor(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	obs := got["uuid-ripple"].Retail
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want tcg's 1 normal + ck's 2 foil: %+v", len(obs), obs)
	}
	for _, o := range obs {
		switch o.Finish {
		case "normal":
			if o.Source != "tcgplayer" || o.Price != 11.86 {
				t.Errorf("normal %+v, want tcgplayer's series", o)
			}
		case "foil":
			if o.Source != "cardkingdom" || (o.Price != 74.99 && o.Price != 75.99) {
				t.Errorf("foil %+v, want cardkingdom's series", o)
			}
		}
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

// When both quote a foil TCGplayer skips, Manapool's marketplace ask beats
// Card Kingdom's premium retail ask — the foil order differs from normal's.
func TestFoilPrefersManapoolOverCardKingdom(t *testing.T) {
	body := `{
 "meta": {"date": "2026-07-29", "version": "5.3.0"},
 "data": {
  "uuid-both": {"paper": {
    "tcgplayer":   {"currency": "USD", "retail": {"normal": {"2026-07-28": 11.86}}},
    "cardkingdom": {"currency": "USD", "retail": {"normal": {"2026-07-27": 12.99},
                                                  "foil": {"2026-07-28": 74.99}}},
    "manapool":    {"currency": "USD", "retail": {"foil": {"2026-07-28": 38.55}}}
  }}
 }
}`
	serve(t, map[string][]byte{
		"/AllPricesToday.json.gz": gzipped(t, body),
		"/AllPrices.json.gz":      gzipped(t, body),
	})

	today, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-both": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	p := today["uuid-both"]
	if p.Foil == nil || *p.Foil != 38.55 || p.FoilSource != "manapool" {
		t.Errorf("foil = %v from %q, want manapool's 38.55", p.Foil, p.FoilSource)
	}
	if p.USD == nil || *p.USD != 11.86 || p.USDSource != "tcgplayer" {
		t.Errorf("normal = %v from %q, want tcgplayer's 11.86", p.USD, p.USDSource)
	}

	hist, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-both": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	for _, o := range hist["uuid-both"].Retail {
		if o.Finish == "foil" && o.Source != "manapool" {
			t.Errorf("foil history %+v, want manapool's series", o)
		}
		if o.Finish == "normal" && o.Source != "tcgplayer" {
			t.Errorf("normal history %+v, want tcgplayer's series", o)
		}
	}
}

// A marketplace's "lowest ask" can be a troll listing — a seven-figure
// Legion Loyalty, observed live. A quote over ListingOutlierRatio times
// the cheapest other vendor's is skipped, today and in the archive; a
// lone vendor is trusted (nothing to compare against).
func TestFoilSkipsTrollListings(t *testing.T) {
	body := `{
 "meta": {"date": "2026-08-02", "version": "5.3.0"},
 "data": {
  "uuid-troll": {"paper": {
    "manapool":    {"currency": "USD", "retail": {"foil": {"2026-08-01": 4741176.74,
                                                           "2026-08-02": 7362059.74}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-08-02": 29.99}}}
  }},
  "uuid-lone": {"paper": {
    "manapool": {"currency": "USD", "retail": {"foil": {"2026-08-02": 500.00}}}
  }}
 }
}`
	serve(t, map[string][]byte{
		"/AllPricesToday.json.gz": gzipped(t, body),
		"/AllPrices.json.gz":      gzipped(t, body),
	})

	today, err := TodayPrices(context.Background(), Options{},
		map[string]bool{"uuid-troll": true, "uuid-lone": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	if p := today["uuid-troll"]; p.Foil == nil || *p.Foil != 29.99 || p.FoilSource != "cardkingdom" {
		t.Errorf("troll-listed foil = %+v, want cardkingdom's 29.99", p)
	}
	if p := today["uuid-lone"]; p.Foil == nil || *p.Foil != 500.00 {
		t.Errorf("lone vendor = %+v, want trusted at 500.00", p)
	}

	hist, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-troll": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	for _, o := range hist["uuid-troll"].Retail {
		if o.Source != "cardkingdom" || o.Price > 1000 {
			t.Errorf("archive obs %+v, want cardkingdom's sane series", o)
		}
	}
}

// The treated-foil overlay: an ExtraSeries merges into the record's
// tcgplayer foil retail before any selection, so a ripple foil the feed
// prices only via other vendors anchors on TCGplayer everywhere — the
// best price, the quote list, and the history's vendor fallback.
func TestExtraSeriesOverlay(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, priceFile)})
	// A figure inside the outlier guard's tolerance of the other vendors —
	// the guard treats a merged series exactly like a native one.
	extra := ExtraSeries{"uuid-ripple": {Foil: map[string]float64{"2026-07-28": 5.55}}}

	got, err := TodayPricesWith(extra)(context.Background(), Options{},
		map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("TodayPricesWith: %v", err)
	}
	p := got["uuid-ripple"]
	if p.Foil == nil || *p.Foil != 5.55 || p.FoilSource != "tcgplayer" {
		t.Errorf("price = %+v, want the overlay's tcgplayer foil figure", p)
	}
	if p.USD == nil || *p.USD != 0.34 {
		t.Errorf("normal = %+v, want the feed's own figure untouched", p)
	}

	qs, err := TodayQuotesWith(extra)(context.Background(), Options{},
		map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("TodayQuotesWith: %v", err)
	}
	var foilQuote *Quote
	for i, q := range qs["uuid-ripple"] {
		if q.Provider == "tcgplayer" && q.Kind == Retail && q.Finish == "foil" {
			foilQuote = &qs["uuid-ripple"][i]
		}
	}
	if foilQuote == nil || foilQuote.Price != 5.55 {
		t.Errorf("quotes = %+v, want a tcgplayer foil quote from the overlay", qs["uuid-ripple"])
	}

	// Without the overlay, nothing changes: the feed's answer stands.
	bare, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	if b := bare["uuid-ripple"]; b.FoilSource == "tcgplayer" {
		t.Errorf("bare price = %+v, want the fallback vendor without the overlay", b)
	}
}

// The history side of the same overlay: the merged series wins the
// per-finish vendor fallback, so a treated foil's back catalogue rides
// TCGplayer's own numbers instead of a marketplace stand-in.
func TestExtraSeriesOverlayHistory(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})
	extra := ExtraSeries{"uuid-no-tcg": {Foil: map[string]float64{"2026-07-27": 16.90, "2026-07-28": 17.56}}}

	got, err := PriceHistoryWith(extra)(context.Background(), Options{},
		map[string]bool{"uuid-no-tcg": true})
	if err != nil {
		t.Fatalf("PriceHistoryWith: %v", err)
	}
	h := got["uuid-no-tcg"]
	var foil []Observation
	for _, o := range h.Retail {
		if o.Finish == "foil" {
			foil = append(foil, o)
		}
	}
	if len(foil) != 2 {
		t.Fatalf("foil series = %+v, want the overlay's two days", foil)
	}
	for _, o := range foil {
		if o.Source != "tcgplayer" {
			t.Errorf("observation %+v, want tcgplayer as the source", o)
		}
	}
}

// TestMain zeroes the request pacer: politeness delays are for the real
// feed, not for httptest.
func TestMain(m *testing.M) {
	requestGap = 0
	os.Exit(m.Run())
}

// The pacer spaces download starts; the day cache never reaches it.
func TestRequestPacing(t *testing.T) {
	oldGap, oldSleep := requestGap, paceSleep
	defer func() {
		requestGap, paceSleep = oldGap, oldSleep
		lastStart = time.Time{}
	}()
	requestGap = time.Minute
	var slept []time.Duration
	paceSleep = func(d time.Duration) { slept = append(slept, d) }
	lastStart = time.Time{}

	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, priceFile)})
	want := map[string]bool{"uuid-tcg": true}
	for range 2 { // uncached: two real downloads, one waited-out gap
		if _, err := TodayPrices(context.Background(), Options{}, want); err != nil {
			t.Fatalf("TodayPrices: %v", err)
		}
	}
	if len(slept) != 1 || slept[0] < 50*time.Second {
		t.Fatalf("slept %v, want one near-full gap between two fresh downloads", slept)
	}

	cached := Options{CacheDir: t.TempDir()}
	if _, err := TodayPrices(context.Background(), cached, want); err != nil {
		t.Fatalf("TodayPrices (fill cache): %v", err)
	}
	n := len(slept)
	if _, err := TodayPrices(context.Background(), cached, want); err != nil {
		t.Fatalf("TodayPrices (cache hit): %v", err)
	}
	if len(slept) != n {
		t.Errorf("a cache hit slept — the pacer must only guard real downloads")
	}
}

// The overlay's two series are different products and must land in different
// buckets. AltProductID used to fall back to the etched id and mergeExtra wrote
// everything into Retail.Foil, so a printing with an etched product and no
// treated foil had the etched product's price filling holes in its foil series
// — one card's price quoted under another's name.
func TestMergeExtraKeepsFoilAndEtchedApart(t *testing.T) {
	var rec priceRecord
	mergeExtra(&rec, ExtraPrices{
		Foil:   map[string]float64{"2026-07-28": 5.55},
		Etched: map[string]float64{"2026-07-28": 24.50},
	})
	v := rec.Paper["tcgplayer"]
	if got := v.Retail.Foil["2026-07-28"]; got != 5.55 {
		t.Errorf("foil = %v, want the treated foil's own price", got)
	}
	if got := v.Retail.Etched["2026-07-28"]; got != 24.50 {
		t.Errorf("etched = %v, want the etched product's own price", got)
	}
}

// An etched-only overlay must not invent a foil price, which is exactly what
// the old single-bucket merge did.
func TestMergeExtraEtchedOnlyLeavesFoilAlone(t *testing.T) {
	var rec priceRecord
	mergeExtra(&rec, ExtraPrices{Etched: map[string]float64{"2026-07-28": 24.50}})
	v := rec.Paper["tcgplayer"]
	if len(v.Retail.Foil) != 0 {
		t.Errorf("foil = %+v, want untouched: this printing has no treated foil", v.Retail.Foil)
	}
	if got := v.Retail.Etched["2026-07-28"]; got != 24.50 {
		t.Errorf("etched = %v, want the etched series", got)
	}
}

// The feed still wins on dates both know; the overlay only fills holes.
func TestMergeExtraDoesNotOverwriteTheFeed(t *testing.T) {
	rec := priceRecord{Paper: map[string]vendor{"tcgplayer": {
		Currency: "USD",
		Retail:   byFinish{Foil: map[string]float64{"2026-07-28": 9.99}},
	}}}
	mergeExtra(&rec, ExtraPrices{Foil: map[string]float64{
		"2026-07-28": 5.55, // the feed already answers this day
		"2026-07-27": 5.40, // this one is a hole
	}})
	v := rec.Paper["tcgplayer"]
	if got := v.Retail.Foil["2026-07-28"]; got != 9.99 {
		t.Errorf("foil = %v, want the feed's own figure to stand", got)
	}
	if got := v.Retail.Foil["2026-07-27"]; got != 5.40 {
		t.Errorf("foil = %v, want the overlay to fill the hole", got)
	}
}

// The two-sided guard, and its asymmetry.
//
// Every figure here was measured live on 2026-08-12. The case that matters is
// the third one: with a cheapest-anchored rule, 0.56 could never be refused
// (nothing is twenty times below itself) and both honest vendors above it were
// themselves rejected as trolls, so the pick fell back to 0.56 — the guard
// protecting the defect it existed to catch.
func TestNonPrice(t *testing.T) {
	cases := []struct {
		name    string
		price   float64
		figures []float64
		want    bool
	}{
		// One voice: nothing to compare against, so nothing is refused.
		{"lone vendor", 500.00, []float64{500.00}, false},

		// Two figures: no majority, so the older rule stands — the cheaper is
		// assumed real and only a wild upward figure is refused.
		{"troll listing, two figures", 7362059.74, []float64{29.99, 7362059.74}, true},
		{"the sane figure beside it", 29.99, []float64{29.99, 7362059.74}, false},
		{"two figures far apart downward", 0.56, []float64{0.56, 120.51}, false},

		// Three figures: both directions measured against the median.
		{"market averaged over no sales", 0.56, []float64{0.56, 59.99, 120.51}, true},
		{"cardkingdom beside it", 59.99, []float64{0.56, 59.99, 120.51}, false},
		{"manapool beside it", 120.51, []float64{0.56, 59.99, 120.51}, false},
		{"troll listing, three figures", 7362059.74, []float64{2.49, 2.99, 7362059.74}, true},
		{"the cheapest beside it", 2.49, []float64{2.49, 2.99, 7362059.74}, false},

		// Ordinary vendor disagreement, which must survive untouched. The last
		// is the regression a cheapest-anchored rule caused: a $1 outlier drags
		// the anchor to $20 and calls a perfectly normal $25 ask a troll.
		{"healthy thin market", 3.89, []float64{3.89, 12.98, 17.36}, false},
		{"worst healthy spread measured", 0.56, []float64{0.56, 1.85}, false},
		{"cheap outlier does not condemn the dear", 25, []float64{1, 15, 25}, false},

		// Nothing to judge.
		{"zero price", 0, []float64{0, 10, 20}, false},
		{"no figures", 5, nil, false},
	}
	for _, tc := range cases {
		if got := NonPrice(tc.price, tc.figures); got != tc.want {
			t.Errorf("NonPrice(%v, %v) = %v, want %v — %s",
				tc.price, tc.figures, got, tc.want, tc.name)
		}
	}
}

// End to end through bestUSD: the shape that started this. TCGplayer's figure
// is an average over no sales, so the foil price comes from the next vendor in
// preference order rather than from the cheapest number on the sheet.
func TestFoilSkipsMarketPriceWithNoSalesBehindIt(t *testing.T) {
	body := `{
 "meta": {"date": "2026-08-12", "version": "5.3.0"},
 "data": {
  "uuid-preorder": {"paper": {
    "tcgplayer":   {"currency": "USD", "retail": {"foil": {"2026-08-11": 0.56}}},
    "manapool":    {"currency": "USD", "retail": {"foil": {"2026-08-11": 120.51}}},
    "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-08-11": 59.99}}}
  }}
 }
}`
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, body)})

	got, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-preorder": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	p := got["uuid-preorder"]
	if p.Foil == nil {
		t.Fatal("no foil price at all; refusing a figure must not unprice the card")
	}
	if *p.Foil == 0.56 {
		t.Error("foil price is still 0.56, the market price no sale stands behind")
	}
	if *p.Foil != 120.51 || p.FoilSource != "manapool" {
		t.Errorf("foil = %v from %q, want manapool's 120.51 (next in foilProviderOrder)",
			*p.Foil, p.FoilSource)
	}
}
