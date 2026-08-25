package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const listingBody = `{"data":[{"type":"default_cards",` +
	`"updated_at":"2026-01-01T00:00:00+00:00",` +
	`"jsonl_download_uri":"http://example.invalid/cards.jsonl",` +
	`"compressed_size":1}]}`

func TestListingFetchGoesThroughTheSharedPacer(t *testing.T) {
	var mu sync.Mutex
	var stamps []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		stamps = append(stamps, time.Now())
		mu.Unlock()
		w.Write([]byte(listingBody))
	}))
	defer srv.Close()

	old := listingURL
	listingURL = srv.URL
	defer func() { listingURL = old }()

	ctx := context.Background()
	for range 2 {
		if _, err := fetchListing(ctx); err != nil {
			t.Fatalf("fetchListing: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stamps) != 2 {
		t.Fatalf("server saw %d requests; want 2", len(stamps))
	}

	const wantGap = 90 * time.Millisecond
	if gap := stamps[1].Sub(stamps[0]); gap < wantGap {
		t.Errorf("two bulk-data listing fetches arrived %v apart; want at least %v — "+
			"the listing is an api.scryfall.com call and must share the pacer with every other one",
			gap, wantGap)
	}
}
