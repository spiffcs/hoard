package action

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// MarketCached reads only the quotes day-cache — the file an earlier
// session's fetch wrote — so a fresh session can show the comparison
// without any network.
func TestArbitrageCachedReadsDayCache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"}, PriceUSD: func() *float64 { v := 2.0; return &v }(),
	}, "nonfoil", 1); err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	d := Deps{Store: st, CacheDir: cacheDir}

	// No cache file yet: not an error, just nothing to serve.
	if _, ok, err := MarketCached(d, 0); err != nil || ok {
		t.Fatalf("no cache: ok=%v err=%v, want a clean miss", ok, err)
	}

	doc := map[string]any{
		"asked": []string{"sol"},
		"quotes": map[string][]mtgjson.Quote{"sol": {
			{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: "normal", Price: 2.00},
			{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: "normal", Price: 2.50},
		}},
	}
	data, _ := json.Marshal(doc)
	path := filepath.Join(cacheDir, time.Now().Format("2006-01-02")+"-owned-quotes.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	res, ok, err := MarketCached(d, 0)
	if err != nil || !ok {
		t.Fatalf("cached read: ok=%v err=%v", ok, err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].SellAt != 2.50 {
		t.Fatalf("result = %+v, want the cached quotes collected", res.Opportunities)
	}
}

// Every quotes read records today's CK bids into the bid history, and a
// repeat read records nothing new. Market always re-parses (F must mean
// fresh — the day cache once made it a silent no-op, which left the
// treated-foil overlay invisible for a day), so the feed itself is served
// here, not a pre-seeded cache file.
func TestMarketRecordsBidQuotes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"},
	}, "nonfoil", 1); err != nil {
		t.Fatal(err)
	}
	// Stamp everything resolve would otherwise fetch a set file to learn.
	if err := st.SaveMTGJSONUUIDs(map[string]string{"sol": "uuid-sol"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCardKingdomLinks(map[string]store.CKLinks{"sol": {}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTCGAltProducts(map[string]string{"sol": ""}, nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/AllPricesToday.json.gz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"data": {"uuid-sol": {"paper": {
			"tcgplayer": {"currency": "USD",
				"retail": {"normal": {"2026-08-01": 2.00}},
				"buylist": {"normal": {"2026-08-01": 9.99}}},
			"cardkingdom": {"currency": "USD",
				"buylist": {"normal": {"2026-08-01": 1.25}}}
		}}}}`))
		zw.Close()
	}))
	defer srv.Close()
	d := Deps{Store: st, CacheDir: t.TempDir(), PriceBaseURL: srv.URL}

	if _, err := Market(context.Background(), d, nil, 0); err != nil {
		t.Fatalf("Market: %v", err)
	}
	series, err := st.BidSeries("sol", "nonfoil")
	if err != nil {
		t.Fatalf("BidSeries: %v", err)
	}
	if len(series) != 1 || series[0].Price != 1.25 || series[0].Source != "cardkingdom" {
		t.Fatalf("bid series = %+v, want the one CK bid as nonfoil", series)
	}

	// A second read is free: the differs-from-last rule drops the repeat.
	if _, err := Market(context.Background(), d, nil, 0); err != nil {
		t.Fatalf("second Market: %v", err)
	}
	if series, _ = st.BidSeries("sol", "nonfoil"); len(series) != 1 {
		t.Errorf("repeat read grew the series to %d rows", len(series))
	}
}

// CardComps serves one card's sheets from the day cache without touching
// the network, and reports a clean miss when no cache exists.
func TestCardCompsReadsDayCache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"},
	}, "nonfoil", 2); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	d := Deps{Store: st, CacheDir: cacheDir}

	if _, ok, err := CardComps(d, "sol"); err != nil || ok {
		t.Fatalf("no cache: ok=%v err=%v, want a clean miss", ok, err)
	}

	doc := map[string]any{
		"asked": []string{"sol"},
		"quotes": map[string][]mtgjson.Quote{"sol": {
			{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: "normal", Price: 2.00},
			{Provider: "manapool", Kind: mtgjson.Retail, Finish: "normal", Price: 2.40},
			{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: "normal", Price: 1.25},
		}},
	}
	data, _ := json.Marshal(doc)
	path := filepath.Join(cacheDir, time.Now().Format("2006-01-02")+"-owned-quotes.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	comps, ok, err := CardComps(d, "sol")
	if err != nil || !ok {
		t.Fatalf("CardComps: ok=%v err=%v", ok, err)
	}
	c, found := comps["nonfoil"]
	if !found || !c.HasMarket || c.Market != 2.00 || !c.HasManapool || !c.HasBuylist {
		t.Fatalf("nonfoil comp = %+v, want the cached sheet", c)
	}
	if c.Card.Copies != 2 {
		t.Errorf("comp carries %d copies, want the held row's 2", c.Card.Copies)
	}
	if _, foil := comps["foil"]; foil {
		t.Errorf("foil sheet from nonfoil-only quotes: %+v", comps)
	}
}
