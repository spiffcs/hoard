package scryfall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func rateLimitedWait(t *testing.T, header string) time.Duration {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header != "" {
			w.Header().Set("Retry-After", header)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, _, err := fetchCollectionChunk(context.Background(), []Identifier{{ID: "sol"}})
	var limited errRateLimited
	if !errors.As(err, &limited) {
		t.Fatalf("a 429 produced %v; want errRateLimited", err)
	}
	return limited.retryAfter
}

func TestRateLimitFallbackIsSecondsNotAMinute(t *testing.T) {
	const ceiling = 10 * time.Second

	wait := rateLimitedWait(t, "")
	if wait > ceiling {
		t.Errorf("a 429 with no Retry-After header asks us to wait %v; "+
			"want at most %v, since the wait is our own guess and not Scryfall's instruction",
			wait, ceiling)
	}
	if wait <= 0 {
		t.Errorf("a 429 with no Retry-After header asks us to wait %v; want a positive pause", wait)
	}
}

func TestRateLimitHonoursRetryAfterHeader(t *testing.T) {
	if wait := rateLimitedWait(t, "3"); wait != 3*time.Second {
		t.Errorf("Retry-After: 3 produced a wait of %v; want 3s", wait)
	}
}

func TestRateLimitWaitsGrowAcrossRetries(t *testing.T) {
	oldBase := rateLimitBase
	rateLimitBase = 20 * time.Millisecond
	defer func() { rateLimitBase = oldBase }()

	var mu sync.Mutex
	var stamps []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		stamps = append(stamps, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if _, _, err := FetchCollection(context.Background(), []Identifier{{ID: "sol"}}); err == nil {
		t.Fatal("a persistent 429 resolved, want an error")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stamps) != rateLimitRetries+1 {
		t.Fatalf("server saw %d requests; want %d", len(stamps), rateLimitRetries+1)
	}

	gaps := make([]time.Duration, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		gaps = append(gaps, stamps[i].Sub(stamps[i-1]))
	}

	if floor := rateLimitBase * 4 / 5; gaps[0] < floor {
		t.Errorf("the first retry waited %v; want at least %v", gaps[0], floor)
	}
	last := gaps[len(gaps)-1]
	if last < 2*gaps[0] {
		t.Errorf("retry waits were %v; the last is %v and the first %v — "+
			"a server still refusing us must be backed off from, not asked again at the same rate",
			gaps, last, gaps[0])
	}
}
