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

func TestPruneCacheSparesInflightTemps(t *testing.T) {
	oldDay := today
	today = func() string { return "2026-08-02" }
	defer func() { today = oldDay }()

	dir := t.TempDir()
	for _, name := range []string{
		"2026-08-01-AllPricesToday.json.gz",
		"2026-08-02-AllPricesToday.json.gz",
		"dl-1234",
		"quotes-5678",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneCache(dir)

	if _, err := os.Stat(filepath.Join(dir, "2026-08-01-AllPricesToday.json.gz")); !os.IsNotExist(err) {
		t.Error("yesterday's entry should have been pruned")
	}
	for _, name := range []string{"2026-08-02-AllPricesToday.json.gz", "dl-1234", "quotes-5678"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should have survived the prune: %v", name, err)
		}
	}
}

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

	if _, err := SetIdentifiers(context.Background(), Options{CacheDir: cacheDir}, "m3c"); err != nil || hits != 1 {
		t.Errorf("second call: err=%v hits=%d, want cached", err, hits)
	}
}

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

	dones = nil
	if _, err := SetIdentifiers(context.Background(), o, "m3c"); err != nil {
		t.Fatalf("SetIdentifiers (cached): %v", err)
	}
	if len(dones) != 0 {
		t.Errorf("Progress fired %d times on a cache hit, want silence", len(dones))
	}
}

func TestStalledDownloadFailsInsteadOfHanging(t *testing.T) {
	oldIdle := idleAfter
	idleAfter = 100 * time.Millisecond
	t.Cleanup(func() { idleAfter = oldIdle })

	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("Content-Length", "1048576")
		w.Write([]byte{0x1f, 0x8b})
		w.(http.Flusher).Flush()
		<-hang
	}))
	t.Cleanup(srv.Close)

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
