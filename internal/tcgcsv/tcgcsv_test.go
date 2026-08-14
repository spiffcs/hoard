package tcgcsv

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, routes map[string]string) Options {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return Options{BaseURL: srv.URL, CacheDir: t.TempDir()}
}

func TestGroups(t *testing.T) {
	o := serve(t, map[string]string{
		"/tcgplayer/1/groups": `{"results": [
			{"groupId": 23445, "abbreviation": "M3C"},
			{"groupId": 23444, "abbreviation": "mh3"},
			{"groupId": 99, "abbreviation": ""}]}`,
	})
	got, err := Groups(context.Background(), o)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if got["M3C"] != 23445 || got["MH3"] != 23444 {
		t.Errorf("groups = %v, want both sets keyed upper-case", got)
	}
	if len(got) != 2 {
		t.Errorf("groups = %v, want the abbreviation-less one dropped", got)
	}
}

func TestGroupPricesPrefersFoilRow(t *testing.T) {
	o := serve(t, map[string]string{
		"/tcgplayer/1/23445/prices": `{"results": [
			{"productId": 553005, "marketPrice": 15.00, "subTypeName": "Normal"},
			{"productId": 553005, "marketPrice": 17.56, "subTypeName": "Foil"},
			{"productId": 552925, "marketPrice": 15.33, "subTypeName": "Normal"},
			{"productId": 500000, "marketPrice": 0, "subTypeName": "Foil"}]}`,
	})
	got, err := GroupPrices(context.Background(), o, 23445)
	if err != nil {
		t.Fatalf("GroupPrices: %v", err)
	}
	if got["553005"].Market != 17.56 {
		t.Errorf("553005 = %v, want the Foil row preferred", got["553005"].Market)
	}
	if got["552925"].Market != 15.33 {
		t.Errorf("552925 = %v, want the Normal-only row standing in", got["552925"].Market)
	}
	if _, ok := got["500000"]; ok {
		t.Error("a zero market price is no price")
	}
}

func TestGroupPricesCarriesAsks(t *testing.T) {
	o := serve(t, map[string]string{
		"/tcgplayer/1/24691/prices": `{"results": [
			{"productId": 707290, "marketPrice": 0.56, "lowPrice": 97.55,
			 "midPrice": 100.07, "highPrice": 435.00, "subTypeName": "Foil"},
			{"productId": 707291, "marketPrice": 4.20, "subTypeName": "Foil"}]}`,
	})
	got, err := GroupPrices(context.Background(), o, 24691)
	if err != nil {
		t.Fatalf("GroupPrices: %v", err)
	}
	want := Quote{Market: 0.56, Low: 97.55, Mid: 100.07, High: 435.00}
	if got["707290"] != want {
		t.Errorf("707290 = %+v, want %+v", got["707290"], want)
	}

	if q := got["707291"]; q.Market != 4.20 || q.Low != 0 {
		t.Errorf("707291 = %+v, want the market price alone", q)
	}
}

func TestDayCache(t *testing.T) {
	oldToday := today
	today = func() string { return "2026-08-02" }
	defer func() { today = oldToday }()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()
	o := Options{BaseURL: srv.URL, CacheDir: t.TempDir()}

	stale := filepath.Join(o.CacheDir, "2026-08-01-groups.json")
	os.WriteFile(stale, []byte("old"), 0o644)

	for range 3 {
		if _, err := Groups(context.Background(), o); err != nil {
			t.Fatalf("Groups: %v", err)
		}
	}
	if hits != 1 {
		t.Errorf("made %d requests across 3 calls, want 1", hits)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("yesterday's entry should have been swept")
	}
}

func TestArchivePrices(t *testing.T) {
	oldRead := readArchiveMembers
	defer func() { readArchiveMembers = oldRead }()
	var extracted []string
	readArchiveMembers = func(_ string, want map[string]bool) (map[string][]byte, error) {
		out := map[string][]byte{}
		for member := range want {
			extracted = append(extracted, member)
			if member == "2026-07-25/1/23445/prices" {
				out[member] = []byte(`{"results": [
					{"productId": 553005, "marketPrice": 16.90, "subTypeName": "Foil"}]}`)
			}
		}
		return out, nil
	}

	var archiveHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/archive/tcgplayer/prices-2026-07-25.ppmd.7z" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		archiveHits++
		w.Write([]byte("7z-bytes"))
	}))
	defer srv.Close()
	o := Options{BaseURL: srv.URL, CacheDir: t.TempDir()}

	got, err := ArchivePrices(context.Background(), o, "2026-07-25", []int{23445, 24554})
	if err != nil {
		t.Fatalf("ArchivePrices: %v", err)
	}
	if got[23445]["553005"].Market != 16.90 {
		t.Errorf("day prices = %v, want the extracted group's figure", got)
	}
	if _, ok := got[24554]; ok {
		t.Error("a group absent from the archive comes back missing, not empty")
	}
	if _, err := os.Stat(filepath.Join(o.CacheDir, "archive", "2026-07-25.7z")); !os.IsNotExist(err) {
		t.Error("the archive should be deleted after extraction")
	}

	extracted = nil
	got, err = ArchivePrices(context.Background(), o, "2026-07-25", []int{23445, 24554})
	if err != nil {
		t.Fatalf("second ArchivePrices: %v", err)
	}
	if archiveHits != 1 || len(extracted) != 0 {
		t.Errorf("second read hit the network %d times and extracted %v, want cache only",
			archiveHits, extracted)
	}
	if got[23445]["553005"].Market != 16.90 {
		t.Errorf("cached day prices = %v", got)
	}
}

func TestArchivePricesRefetchesUnparseableCache(t *testing.T) {
	oldRead := readArchiveMembers
	defer func() { readArchiveMembers = oldRead }()
	readArchiveMembers = func(_ string, want map[string]bool) (map[string][]byte, error) {
		out := map[string][]byte{}
		for member := range want {
			out[member] = []byte(`{"results": [
				{"productId": 553005, "marketPrice": 16.90, "subTypeName": "Foil"}]}`)
		}
		return out, nil
	}
	var archiveHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		archiveHits++
		w.Write([]byte("7z-bytes"))
	}))
	defer srv.Close()
	o := Options{BaseURL: srv.URL, CacheDir: t.TempDir()}

	dir := filepath.Join(o.CacheDir, "archive")
	os.MkdirAll(dir, 0o755)
	torn := filepath.Join(dir, "2026-07-25-23445.json")
	os.WriteFile(torn, []byte(`{"results": [{"productId": 5`), 0o644)

	got, err := ArchivePrices(context.Background(), o, "2026-07-25", []int{23445})
	if err != nil {
		t.Fatalf("ArchivePrices: %v", err)
	}
	if archiveHits != 1 {
		t.Errorf("archive downloads = %d, want the torn cache file re-fetched", archiveHits)
	}
	if got[23445]["553005"].Market != 16.90 {
		t.Errorf("prices = %v, want the re-extracted figure", got)
	}

	b, err := os.ReadFile(torn)
	if err != nil || !strings.Contains(string(b), "553005") {
		t.Errorf("cache file after refetch = %q, %v; want the good extraction", b, err)
	}
}

func TestLenientPPMdRefusesImplausibleHeaders(t *testing.T) {
	reader := func() []io.ReadCloser {
		return []io.ReadCloser{io.NopCloser(bytes.NewReader(nil))}
	}
	for _, tc := range []struct {
		name  string
		props []byte
		size  uint64
	}{
		{"order too small", []byte{1, 0, 0, 0, 1}, 10},
		{"order too big", []byte{65, 0, 0, 0, 1}, 10},
		{"memory over the ceiling", []byte{8, 0xFF, 0xFF, 0xFF, 0xFF}, 10},
		{"output over the ceiling", []byte{8, 0, 0, 0, 1}, 2 << 30},
	} {
		if _, err := lenientPPMd(tc.props, tc.size, reader()); err == nil ||
			!strings.Contains(err.Error(), "implausible") {
			t.Errorf("%s: err = %v, want the implausible-header refusal", tc.name, err)
		}
	}
}

func TestLenientPPMdProperties(t *testing.T) {
	one := []io.ReadCloser{io.NopCloser(bytes.NewReader(nil))}
	if _, err := lenientPPMd([]byte{8, 0, 0}, 10, one); err == nil ||
		!strings.Contains(err.Error(), "not enough properties") {
		t.Errorf("err = %v, want genuinely short properties refused", err)
	}

	if _, err := lenientPPMd([]byte{8, 0, 0, 0, 1, 0, 0}, 10, nil); err == nil ||
		!strings.Contains(err.Error(), "one reader") {
		t.Errorf("err = %v, want the reader-count refusal", err)
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	bare := Options{BaseURL: srv.URL}
	for range 2 {
		if _, err := Groups(context.Background(), bare); err != nil {
			t.Fatalf("Groups: %v", err)
		}
	}
	if len(slept) != 1 || slept[0] < 50*time.Second {
		t.Fatalf("slept %v, want one near-full gap between two fresh requests", slept)
	}

	cached := Options{BaseURL: srv.URL, CacheDir: t.TempDir()}
	if _, err := Groups(context.Background(), cached); err != nil {
		t.Fatalf("Groups (fill cache): %v", err)
	}
	n := len(slept)
	if _, err := Groups(context.Background(), cached); err != nil {
		t.Fatalf("Groups (cache hit): %v", err)
	}
	if len(slept) != n {
		t.Errorf("a cache hit slept — the pacer must only guard real requests")
	}
}
