package action

import (
	"encoding/json"
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
