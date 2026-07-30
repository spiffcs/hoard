package mtgjson

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 200 that is not gzip — a captive portal, an outage page — must not become
// today's cache entry: cached, it would wedge every price command until
// midnight with an error naming neither the file nor the fix.
func TestNonGzipResponseIsNotCached(t *testing.T) {
	serve(t, map[string][]byte{"/M3C.json.gz": []byte("<html>service maintenance</html>")})
	cacheDir := t.TempDir()

	_, err := SetIdentifiers(context.Background(), cacheDir, "m3c")
	if err == nil {
		t.Fatal("a non-gzip 200 resolved, want an error")
	}
	if !strings.Contains(err.Error(), "non-gzip") {
		t.Errorf("err = %v, want it to name the non-gzip response", err)
	}
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json.gz") {
			t.Errorf("the bad response was cached as %s", e.Name())
		}
	}
}

// A poisoned entry already in the cache — from a version without the write
// gate, or a torn disk — heals itself: deleted and re-downloaded, rather than
// wedging the day.
func TestPoisonedCacheEntryIsReplaced(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(gzipped(t, m3cFile))
	}))
	defer srv.Close()
	oldBase := apiBase
	apiBase = srv.URL
	defer func() { apiBase = oldBase }()

	cacheDir := t.TempDir()
	poisoned := filepath.Join(cacheDir, today()+"-M3C.json.gz")
	if err := os.WriteFile(poisoned, []byte("<html>not gzip</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := SetIdentifiers(context.Background(), cacheDir, "m3c")
	if err != nil {
		t.Fatalf("SetIdentifiers over a poisoned cache: %v", err)
	}
	if hits != 1 || len(got) != 2 {
		t.Errorf("hits=%d ids=%d, want a single re-download resolving both ids", hits, len(got))
	}
	// And the healed entry serves the next call without the network.
	if _, err := SetIdentifiers(context.Background(), cacheDir, "m3c"); err != nil || hits != 1 {
		t.Errorf("second call: err=%v hits=%d, want cached", err, hits)
	}
}
