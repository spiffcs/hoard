package scryfall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	slowGap = 0
	defaultGap = 0
	os.Exit(m.Run())
}

func TestEndpointClass(t *testing.T) {
	slow := []string{
		"https://api.scryfall.com/cards/search?q=x",
		"https://api.scryfall.com/cards/search?unique=prints&page=2",
		"https://api.scryfall.com/cards/named?fuzzy=sol+ring",
		"https://api.scryfall.com/cards/collection",
		"http://127.0.0.1:9999/cards/search?q=x",
	}
	for _, ep := range slow {
		if class, _ := endpointClass(ep); class == "" {
			t.Errorf("endpointClass(%q) fell to the default class; want a 2/second class", ep)
		}
	}
	fast := []string{
		"https://api.scryfall.com/cards/autocomplete?q=sol",
		"https://api.scryfall.com/cards/uma/7",
	}
	for _, ep := range fast {
		if class, _ := endpointClass(ep); class != "" {
			t.Errorf("endpointClass(%q) = %q; want the default class", ep, class)
		}
	}
}

func TestPacerSpacesSlowEndpointRequests(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 50*time.Millisecond, 0
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()
	apiPacer = pacer{next: map[string]time.Time{}}

	var stamps []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stamps = append(stamps, time.Now())
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	for range 3 {
		if _, err := NamedFuzzy(context.Background(), "sol ring"); err != nil {
			t.Fatalf("NamedFuzzy: %v", err)
		}
	}
	if len(stamps) != 3 {
		t.Fatalf("server saw %d requests; want 3", len(stamps))
	}
	for i := 1; i < len(stamps); i++ {
		if gap := stamps[i].Sub(stamps[i-1]); gap < 45*time.Millisecond {
			t.Errorf("requests %d and %d arrived %v apart; want at least ~50ms", i-1, i, gap)
		}
	}
}
