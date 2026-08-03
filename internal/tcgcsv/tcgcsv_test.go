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

// Groups keys TCGplayer's group list by upper-cased abbreviation — the
// join to Scryfall set codes.
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

// GroupPrices prefers each product's Foil row — a treated product usually
// lists only as Foil, but a Normal-only row still describes the treated
// product and stands in.
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
	if got["553005"] != 17.56 {
		t.Errorf("553005 = %v, want the Foil row preferred", got["553005"])
	}
	if got["552925"] != 15.33 {
		t.Errorf("552925 = %v, want the Normal-only row standing in", got["552925"])
	}
	if _, ok := got["500000"]; ok {
		t.Error("a zero market price is no price")
	}
}

// The day cache: one download per day per file, and yesterday's entries
// sweep on the next write.
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

// ArchivePrices extracts only the requested groups from a day's archive,
// keeps the extractions (immutable, date-keyed), deletes the archive, and
// records absent groups so the next run does not re-download to relearn
// an absence.
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
	if got[23445]["553005"] != 16.90 {
		t.Errorf("day prices = %v, want the extracted group's figure", got)
	}
	if _, ok := got[24554]; ok {
		t.Error("a group absent from the archive comes back missing, not empty")
	}
	if _, err := os.Stat(filepath.Join(o.CacheDir, "archive", "2026-07-25.7z")); !os.IsNotExist(err) {
		t.Error("the archive should be deleted after extraction")
	}

	// A second read serves both groups from the extraction cache: no
	// download, no extraction — the absence marker answers for 24554.
	extracted = nil
	got, err = ArchivePrices(context.Background(), o, "2026-07-25", []int{23445, 24554})
	if err != nil {
		t.Fatalf("second ArchivePrices: %v", err)
	}
	if archiveHits != 1 || len(extracted) != 0 {
		t.Errorf("second read hit the network %d times and extracted %v, want cache only",
			archiveHits, extracted)
	}
	if got[23445]["553005"] != 16.90 {
		t.Errorf("cached day prices = %v", got)
	}
}

// The lenient PPMd decompressor accepts the seven-byte coder properties
// the tcgcsv archives carry (five canonical bytes plus trailing zeros)
// and still refuses genuinely short ones.
func TestLenientPPMdProperties(t *testing.T) {
	one := []io.ReadCloser{io.NopCloser(bytes.NewReader(nil))}
	if _, err := lenientPPMd([]byte{8, 0, 0}, 10, one); err == nil ||
		!strings.Contains(err.Error(), "not enough properties") {
		t.Errorf("err = %v, want genuinely short properties refused", err)
	}
	// The archives' seven-byte shape clears the property check; a wrong
	// reader count then refuses, which exercises the parse without a real
	// PPMd stream.
	if _, err := lenientPPMd([]byte{8, 0, 0, 0, 1, 0, 0}, 10, nil); err == nil ||
		!strings.Contains(err.Error(), "one reader") {
		t.Errorf("err = %v, want the reader-count refusal", err)
	}
}

// TestMain zeroes the request pacer: politeness delays are for the real
// mirror, not for httptest.
func TestMain(m *testing.M) {
	requestGap = 0
	os.Exit(m.Run())
}

// The pacer spaces request starts and never touches a cache hit.
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

	// Uncached back-to-back requests: the first rides free, the second
	// waits out the gap.
	bare := Options{BaseURL: srv.URL}
	for range 2 {
		if _, err := Groups(context.Background(), bare); err != nil {
			t.Fatalf("Groups: %v", err)
		}
	}
	if len(slept) != 1 || slept[0] < 50*time.Second {
		t.Fatalf("slept %v, want one near-full gap between two fresh requests", slept)
	}

	// A cache hit answers from disk: no request, no wait.
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
