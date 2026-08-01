package mtgjson

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A 200 that is not gzip — a captive portal, an outage page — must not become
// today's cache entry: cached, it would wedge every price command until
// midnight with an error naming neither the file nor the fix.
func TestNonGzipResponseIsNotCached(t *testing.T) {
	serve(t, map[string][]byte{"/M3C.json.gz": []byte("<html>service maintenance</html>")})
	cacheDir := t.TempDir()

	_, err := SetIdentifiers(context.Background(), Options{CacheDir: cacheDir}, "m3c")
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

	got, err := SetIdentifiers(context.Background(), Options{CacheDir: cacheDir}, "m3c")
	if err != nil {
		t.Fatalf("SetIdentifiers over a poisoned cache: %v", err)
	}
	if hits != 1 || len(got) != 2 {
		t.Errorf("hits=%d ids=%d, want a single re-download resolving both ids", hits, len(got))
	}
	// And the healed entry serves the next call without the network.
	if _, err := SetIdentifiers(context.Background(), Options{CacheDir: cacheDir}, "m3c"); err != nil || hits != 1 {
		t.Errorf("second call: err=%v hits=%d, want cached", err, hits)
	}
}

// Options.Progress sees the download land: cumulative bytes against the
// Content-Length total — and nothing at all on a cache hit, where there is
// no wait worth narrating.
func TestProgressReportsDownloadBytes(t *testing.T) {
	payload := gzipped(t, `{"data": {}}`)
	serve(t, map[string][]byte{"/M3C.json.gz": payload})
	cacheDir := t.TempDir()

	var dones []int64
	var lastTotal int64
	o := Options{CacheDir: cacheDir, Progress: func(done, total int64) {
		dones = append(dones, done)
		lastTotal = total
	}}
	if _, err := SetIdentifiers(context.Background(), o, "m3c"); err != nil {
		t.Fatalf("SetIdentifiers: %v", err)
	}
	if len(dones) == 0 {
		t.Fatal("Progress never fired during a genuine download")
	}
	if got := dones[len(dones)-1]; got != int64(len(payload)) {
		t.Errorf("final done = %d, want the full body (%d)", got, len(payload))
	}
	if lastTotal != int64(len(payload)) {
		t.Errorf("total = %d, want Content-Length (%d)", lastTotal, len(payload))
	}

	// Second read is a cache hit: silent.
	dones = nil
	if _, err := SetIdentifiers(context.Background(), o, "m3c"); err != nil {
		t.Fatalf("SetIdentifiers (cached): %v", err)
	}
	if len(dones) != 0 {
		t.Errorf("Progress fired %d times on a cache hit, want silence", len(dones))
	}
}

// A download that stops delivering bytes must fail with a clear error, not
// hang forever — the pre-timeout behavior for a silently dead connection
// under a ~150 MB archive.
func TestStalledDownloadFailsInsteadOfHanging(t *testing.T) {
	oldIdle := idleAfter
	idleAfter = 100 * time.Millisecond
	t.Cleanup(func() { idleAfter = oldIdle })

	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A few real bytes, then silence: the connection is alive, the data
		// is not coming.
		w.Header().Set("Content-Length", "1048576")
		w.Write([]byte{0x1f, 0x8b})
		w.(http.Flusher).Flush()
		<-hang
	}))
	t.Cleanup(srv.Close)
	// Registered after srv.Close so it runs first (cleanups are LIFO):
	// Close waits for the handler, and the handler waits for this.
	t.Cleanup(func() { close(hang) })
	oldBase := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = oldBase })

	done := make(chan error, 1)
	go func() {
		_, err := SetIdentifiers(context.Background(), Options{CacheDir: t.TempDir()}, "m3c")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Errorf("err = %v, want a stall error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled download still hanging after 5s — the idle timeout did not fire")
	}
}
