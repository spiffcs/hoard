package scryfall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A stray 502 mid-run must not discard the chunks already fetched: a big
// import is dozens of requests, and before this retry a single transient
// failure on the last one threw away minutes of resolution.
func TestFetchCollectionRetriesTransientFailures(t *testing.T) {
	oldPause := transientPause
	transientPause = time.Millisecond
	defer func() { transientPause = oldPause }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"data":[{"id":"sol","name":"Sol Ring","set":"c21","collector_number":"125"}],"not_found":[]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	found, _, err := FetchCollection(context.Background(), []Identifier{{ID: "sol"}})
	if err != nil {
		t.Fatalf("FetchCollection: %v", err)
	}
	if calls != 2 || len(found) != 1 || found[0].Name != "Sol Ring" {
		t.Errorf("calls=%d found=%v, want one retry then success", calls, found)
	}
}

// A 4xx is the caller's mistake, not weather — retrying it would only repeat
// the same answer three times slower.
func TestFetchCollectionDoesNotRetryClientErrors(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if _, _, err := FetchCollection(context.Background(), []Identifier{{ID: "x"}}); err == nil {
		t.Fatal("a 400 resolved, want an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want no retries on a 4xx", calls)
	}
}

// A persistent outage still fails after the bounded attempts, with the real
// error — not a hang, and not a success with missing cards.
func TestFetchCollectionGivesUpAfterBoundedRetries(t *testing.T) {
	oldPause := transientPause
	transientPause = time.Millisecond
	defer func() { transientPause = oldPause }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if _, _, err := FetchCollection(context.Background(), []Identifier{{ID: "x"}}); err == nil {
		t.Fatal("a persistent 503 resolved, want an error")
	}
	if calls != rateLimitRetries+1 {
		t.Errorf("calls = %d, want %d bounded attempts", calls, rateLimitRetries+1)
	}
}
