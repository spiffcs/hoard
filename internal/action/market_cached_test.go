package action

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/spiffcs/hoard/internal/finish"
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

func TestArbitrageCachedReadsDayCache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"}, PriceUSD: func() *float64 { v := 2.0; return &v }(),
	}, finish.Nonfoil, 1); err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	d := Deps{Store: st, CacheDir: cacheDir}

	if _, ok, err := MarketCached(d, 0); err != nil || ok {
		t.Fatalf("no cache: ok=%v err=%v, want a clean miss", ok, err)
	}

	doc := map[string]any{
		"asked": []string{"sol"},
		"quotes": map[string][]mtgjson.Quote{"sol": {
			{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Nonfoil, Price: 2.00},
			{Provider: "tcgplayer", Kind: mtgjson.Ask, Finish: finish.Nonfoil, Price: 2.00},
			{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: finish.Nonfoil, Price: 2.50},
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

func TestMarketRecordsBidQuotes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"},
	}, finish.Nonfoil, 1); err != nil {
		t.Fatal(err)
	}

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
	series, err := st.BidSeries("sol", finish.Nonfoil)
	if err != nil {
		t.Fatalf("BidSeries: %v", err)
	}
	if len(series) != 1 || series[0].Price != 1.25 || series[0].Source != "cardkingdom" {
		t.Fatalf("bid series = %+v, want the one CK bid as nonfoil", series)
	}

	if _, err := Market(context.Background(), d, nil, 0); err != nil {
		t.Fatalf("second Market: %v", err)
	}
	if series, _ = st.BidSeries("sol", finish.Nonfoil); len(series) != 1 {
		t.Errorf("repeat read grew the series to %d rows", len(series))
	}
}

func TestCardCompsReadsDayCache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "sol", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finishes: []string{"nonfoil"},
	}, finish.Nonfoil, 2); err != nil {
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
			{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Nonfoil, Price: 2.00},
			{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Nonfoil, Price: 2.40},
			{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: finish.Nonfoil, Price: 1.25},
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
	c, found := comps[finish.Nonfoil]
	if !found || !c.HasMarket || c.Market != 2.00 || !c.HasManapool || !c.HasBuylist {
		t.Fatalf("nonfoil comp = %+v, want the cached sheet", c)
	}
	if c.Card.Copies != 2 {
		t.Errorf("comp carries %d copies, want the held row's 2", c.Card.Copies)
	}
	if _, foil := comps[finish.Foil]; foil {
		t.Errorf("foil sheet from nonfoil-only quotes: %+v", comps)
	}
}

func writeQuoteCache(t *testing.T, dir string, quotes map[string][]mtgjson.Quote) {
	t.Helper()
	asked := make([]string, 0, len(quotes))
	for id := range quotes {
		asked = append(asked, id)
	}
	data, err := json.Marshal(map[string]any{"asked": asked, "quotes": quotes})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, time.Now().Format("2006-01-02")+"-owned-quotes.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCardCompsNoEtchedSheetWithoutAnEtchedProduct(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "bitter", Name: "Bitterblossom", Set: "uma", CollectorNumber: "85",
		Finishes: []string{"nonfoil", "foil"},
	}, finish.Nonfoil, 1); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	writeQuoteCache(t, cacheDir, map[string][]mtgjson.Quote{"bitter": {
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Nonfoil, Price: 34.47},
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 45.56},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 54.99},
		{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: finish.Foil, Price: 27.50},
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 52.98},
	}})

	comps, ok, err := CardComps(Deps{Store: st, CacheDir: cacheDir}, "bitter")
	if err != nil || !ok {
		t.Fatalf("CardComps: ok=%v err=%v", ok, err)
	}
	if _, foil := comps[finish.Foil]; !foil {
		t.Fatalf("comps = %+v, want the foil sheet the feed does quote", comps)
	}
	if c, etched := comps[finish.Etched]; etched {
		t.Errorf("etched sheet for a nonfoil/foil printing: %+v", c)
	}
}

func TestCardCompsKeepsARealEtchedSheet(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddCardFinish(scryfall.Card{
		ID: "kaalia", Name: "Kaalia of the Vast", Set: "mh3", CollectorNumber: "489",
		Finishes: []string{"etched"},
	}, finish.Etched, 1); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	writeQuoteCache(t, cacheDir, map[string][]mtgjson.Quote{"kaalia": {
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 11.55},
		{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Etched, Price: 11.55},
		{Provider: "cardkingdom", Kind: mtgjson.Retail, Finish: finish.Etched, Price: 6.99},
		{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: finish.Etched, Price: 3.50},
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Etched, Price: 10.31},
	}})

	comps, ok, err := CardComps(Deps{Store: st, CacheDir: cacheDir}, "kaalia")
	if err != nil || !ok {
		t.Fatalf("CardComps: ok=%v err=%v", ok, err)
	}
	c, etched := comps[finish.Etched]
	if !etched {
		t.Fatalf("comps = %+v, want the etched sheet for an etched printing", comps)
	}
	if !c.HasCK || c.CK != 6.99 || !c.HasBuylist || c.Buylist != 3.50 {
		t.Errorf("etched comp = %+v, want the etched product's own quotes", c)
	}
	if c.Card.Copies != 1 {
		t.Errorf("etched comp carries %d copies, want the held row's 1", c.Card.Copies)
	}
}
