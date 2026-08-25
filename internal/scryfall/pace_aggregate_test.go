package scryfall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPacerBoundsTheAggregateRequestRate(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 60*time.Millisecond, 20*time.Millisecond
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	apiPacer = pacer{next: map[string]time.Time{}}
	defer func() { apiPacer = pacer{next: map[string]time.Time{}} }()

	var mu sync.Mutex
	var stamps []time.Time
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		stamps = append(stamps, time.Now())
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	ctx := context.Background()
	for range 3 {
		if _, err := NamedFuzzy(ctx, "sol ring"); err != nil {
			t.Fatalf("NamedFuzzy: %v", err)
		}
		if _, err := Autocomplete(ctx, "sol"); err != nil {
			t.Fatalf("Autocomplete: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stamps) != 6 {
		t.Fatalf("server saw %d requests; want 6", len(stamps))
	}

	const tolerance = 3 * time.Millisecond
	for i := 1; i < len(stamps); i++ {
		gap := stamps[i].Sub(stamps[i-1])
		if gap+tolerance < defaultGap {
			t.Errorf("requests %d (%s) and %d (%s) arrived %v apart; "+
				"no two API requests may be closer than %v whatever their endpoint",
				i-1, paths[i-1], i, paths[i], gap, defaultGap)
		}
	}
}
