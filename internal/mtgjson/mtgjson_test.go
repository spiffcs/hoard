package mtgjson

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"github.com/spiffcs/hoard/internal/finish"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

	if p := got["uuid-tcg"]; p.FoilSource != "tcgplayer" || p.Foil == nil || *p.Foil != 11.16 {
		t.Errorf("uuid-tcg = %+v, want tcgplayer 11.16", p)
	}

	p := got["uuid-ck"]
	if p.FoilSource != "manapool" || p.Foil == nil || *p.Foil != 6.91 {
		t.Errorf("uuid-ck = %+v, want manapool foil 6.91", p)
	}
	if p.USD == nil || *p.USD != 0.34 || p.USDSource != "cardkingdom" {
		t.Errorf("uuid-ck normal = %v from %q, want 0.34 from cardkingdom", p.USD, p.USDSource)
	}

	if _, ok := got["uuid-eur-only"]; ok {
		t.Error("a EUR-only card must not be priced")
	}

	if p := got["uuid-stale"]; p.Foil == nil || *p.Foil != 2.50 {
		t.Errorf("uuid-stale = %+v, want the 2026-07-28 price", p)
	}

	if _, ok := got["uuid-unwanted"]; ok {
		t.Error("returned a card that was not requested")
	}
	if len(got) != 3 {
		t.Errorf("got %d prices, want 3", len(got))
	}
}

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

	byKey := map[string]float64{}
	for _, q := range qs {
		byKey[q.Provider+"/"+q.Kind+"/"+q.Finish.String()] = q.Price
	}
	for key, want := range map[string]float64{
		"cardkingdom/retail/nonfoil": 0.99,
		"cardkingdom/retail/foil":    2.49,
		"cardkingdom/buylist/foil":   0.75,
		"tcgplayer/retail/nonfoil":   0.42,
		"manapool/retail/nonfoil":    0.20,
		"manapool/retail/foil":       138518.78,
	} {
		if byKey[key] != want {
			t.Errorf("%s = %v, want %v", key, byKey[key], want)
		}
	}

	if byKey["cardkingdom/buylist/foil"] == 0 {
		t.Error("buylist quotes must be included")
	}

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
	apiBase = "http://127.0.0.1:1"
	defer func() { apiBase = old }()

	got, err := TodayQuotes(context.Background(), Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("TodayQuotes(nil) = %v, %v; want empty with no request", got, err)
	}
}

func TestTodayPricesEmptyRequestSkipsDownload(t *testing.T) {

	old := apiBase
	apiBase = "http://127.0.0.1:1"
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

	got, err := SetIdentifiers(context.Background(), Options{}, "m3c")
	if err != nil {
		t.Fatalf("SetIdentifiers: %v", err)
	}
	if got["scry-1"].UUID != "uuid-ck" || got["scry-2"].UUID != "uuid-tcg" {
		t.Errorf("map = %v, want both Scryfall IDs mapped", got)
	}

	if got["scry-1"].CKURL != "https://mtgjson.com/links/aa" ||
		got["scry-1"].CKFoilURL != "https://mtgjson.com/links/bb" {
		t.Errorf("links = %+v, want both finishes", got["scry-1"])
	}
	if got["scry-2"].CKURL != "" || got["scry-2"].CKFoilURL != "" {
		t.Errorf("linkless card = %+v, want empty links", got["scry-2"])
	}

	if len(got) != 2 {
		t.Errorf("map has %d entries, want 2", len(got))
	}

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

	_, err := SetIdentifiers(context.Background(), Options{}, "nope")
	if !errors.Is(err, ErrNoSuchSet) {
		t.Errorf("err = %v, want ErrNoSuchSet", err)
	}
}

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

	if len(obs) != 5 {
		t.Fatalf("got %d observations, want 5: %+v", len(obs), obs)
	}
	for _, o := range obs {
		if o.Source != "tcgplayer" {
			t.Errorf("observation %+v: source = %q, want tcgplayer", o, o.Source)
		}

		if o.Price == 9.99 || o.Price == 0.40 || o.Price == 0.75 || o.Price == 0.80 || o.Price == 2.00 {
			t.Errorf("observation %+v leaked from another vendor or the buylist", o)
		}
	}
}

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

func TestPriceHistoryFoilFallsToNextVendor(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	obs := got["uuid-ripple"].Retail
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want tcg's 1 nonfoil + ck's 2 foil: %+v", len(obs), obs)
	}
	for _, o := range obs {
		switch o.Finish {
		case finish.Nonfoil:
			if o.Source != "tcgplayer" || o.Price != 11.86 {
				t.Errorf("nonfoil %+v, want tcgplayer's series", o)
			}
		case finish.Foil:
			if o.Source != "cardkingdom" || (o.Price != 74.99 && o.Price != 75.99) {
				t.Errorf("foil %+v, want cardkingdom's series", o)
			}
		}
	}
}

func TestPriceHistoryEmptyRequestSkipsDownload(t *testing.T) {
	old := apiBase
	apiBase = "http://127.0.0.1:1"
	defer func() { apiBase = old }()

	got, err := PriceHistory(context.Background(), Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty and no error", got, err)
	}
}

func TestUnusableCacheDirStillServes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(gzipped(t, priceFile))
	}))
	defer srv.Close()
	oldBase := apiBase
	apiBase = srv.URL
	defer func() { apiBase = oldBase }()

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
		if o.Finish == finish.Foil && o.Source != "manapool" {
			t.Errorf("foil history %+v, want manapool's series", o)
		}
		if o.Finish == finish.Nonfoil && o.Source != "tcgplayer" {
			t.Errorf("normal history %+v, want tcgplayer's series", o)
		}
	}
}

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

func TestExtraSeriesOverlay(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, priceFile)})

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
		if q.Provider == "tcgplayer" && q.Kind == Retail && q.Finish == finish.Foil {
			foilQuote = &qs["uuid-ripple"][i]
		}
	}
	if foilQuote == nil || foilQuote.Price != 5.55 {
		t.Errorf("quotes = %+v, want a tcgplayer foil quote from the overlay", qs["uuid-ripple"])
	}

	bare, err := TodayPrices(context.Background(), Options{}, map[string]bool{"uuid-ripple": true})
	if err != nil {
		t.Fatalf("TodayPrices: %v", err)
	}
	if b := bare["uuid-ripple"]; b.FoilSource == "tcgplayer" {
		t.Errorf("bare price = %+v, want the fallback vendor without the overlay", b)
	}
}

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
		if o.Finish == finish.Foil {
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

func TestMain(m *testing.M) {
	requestGap = 0
	os.Exit(m.Run())
}

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
	for range 2 {
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

func TestMergeExtraDoesNotOverwriteTheFeed(t *testing.T) {
	rec := priceRecord{Paper: map[string]vendor{"tcgplayer": {
		Currency: "USD",
		Retail:   byFinish{Foil: map[string]float64{"2026-07-28": 9.99}},
	}}}
	mergeExtra(&rec, ExtraPrices{Foil: map[string]float64{
		"2026-07-28": 5.55,
		"2026-07-27": 5.40,
	}})
	v := rec.Paper["tcgplayer"]
	if got := v.Retail.Foil["2026-07-28"]; got != 9.99 {
		t.Errorf("foil = %v, want the feed's own figure to stand", got)
	}
	if got := v.Retail.Foil["2026-07-27"]; got != 5.40 {
		t.Errorf("foil = %v, want the overlay to fill the hole", got)
	}
}

func TestNonPrice(t *testing.T) {
	cases := []struct {
		name    string
		price   float64
		figures []float64
		want    bool
	}{

		{"lone vendor", 500.00, []float64{500.00}, false},

		{"troll listing, two figures", 7362059.74, []float64{29.99, 7362059.74}, true},
		{"the sane figure beside it", 29.99, []float64{29.99, 7362059.74}, false},
		{"two figures far apart downward", 0.56, []float64{0.56, 120.51}, false},

		{"market averaged over no sales", 0.56, []float64{0.56, 59.99, 120.51}, true},
		{"cardkingdom beside it", 59.99, []float64{0.56, 59.99, 120.51}, false},
		{"manapool beside it", 120.51, []float64{0.56, 59.99, 120.51}, false},
		{"troll listing, three figures", 7362059.74, []float64{2.49, 2.99, 7362059.74}, true},
		{"the cheapest beside it", 2.49, []float64{2.49, 2.99, 7362059.74}, false},

		{"healthy thin market", 3.89, []float64{3.89, 12.98, 17.36}, false},
		{"worst healthy spread measured", 0.56, []float64{0.56, 1.85}, false},
		{"cheap outlier does not condemn the dear", 25, []float64{1, 15, 25}, false},

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
