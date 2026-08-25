package action

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

const archiveForSol = `{"data": {"uuid-sol": {"paper": {"tcgplayer": {
	"currency": "USD",
	"retail": {"normal": {
		"2026-08-20": 2.00, "2026-08-21": 2.10, "2026-08-22": 2.25,
		"2026-08-23": 2.20, "2026-08-24": 2.40}}}}}}}`

func backfillFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
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
	if err := st.SaveVendorProductIDs(map[string]store.VendorProductIDs{"sol": {}}); err != nil {
		t.Fatal(err)
	}
	return st, t.TempDir()
}

func writeCachedArchive(t *testing.T, cacheDir, day, body string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	path := filepath.Join(cacheDir, day+"-AllPrices.json.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillPrintingsFillsFromTheCachedArchive(t *testing.T) {
	st, cacheDir := backfillFixture(t)
	writeCachedArchive(t, cacheDir, time.Now().Format("2006-01-02"), archiveForSol)

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("went to the network for %s despite a cached archive", r.URL.Path)
	}))
	defer srv.Close()

	d := Deps{Store: st, CacheDir: cacheDir, PriceBaseURL: srv.URL}
	refs := []pricing.Ref{{ScryfallID: "sol", SetCode: "c21", Finish: finish.Nonfoil}}

	n, err := BackfillPrintings(context.Background(), d, refs, 90)
	if err != nil {
		t.Fatalf("BackfillPrintings: %v", err)
	}
	if n == 0 {
		t.Error("nothing was backfilled from an archive that has five days of prices")
	}

	series, err := st.PriceSeries("sol", finish.Nonfoil)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) < 5 {
		t.Errorf("sparkline has %d points, want the archive's five days", len(series))
	}
}

func TestBackfillPrintingsSkipsWhenTheArchiveIsNotCached(t *testing.T) {
	st, cacheDir := backfillFixture(t)

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("started a 150MB download from a cold cache: %s", r.URL.Path)
	}))
	defer srv.Close()

	d := Deps{Store: st, CacheDir: cacheDir, PriceBaseURL: srv.URL}
	refs := []pricing.Ref{{ScryfallID: "sol", SetCode: "c21", Finish: finish.Nonfoil}}

	n, err := BackfillPrintings(context.Background(), d, refs, 90)
	if err != nil {
		t.Fatalf("a cold cache must be a quiet no-op, got %v", err)
	}
	if n != 0 {
		t.Errorf("backfilled %d points with no archive on disk", n)
	}

	series, err := st.PriceSeries("sol", finish.Nonfoil)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 0 {
		t.Errorf("sparkline gained %d points from nowhere", len(series))
	}
}
