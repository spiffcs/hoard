package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func useBase(t *testing.T, url string) {
	t.Helper()
	prev := releasesBaseURL
	releasesBaseURL = url
	t.Cleanup(func() { releasesBaseURL = prev })
}

func redirectingTo(t *testing.T, location string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestReadsTheTagFromTheLocationHeader(t *testing.T) {
	srv := redirectingTo(t, "https://github.com/spiffcs/hoard/releases/tag/v0.4.1")
	useBase(t, srv.URL)

	got, err := Latest(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "v0.4.1" {
		t.Errorf("Latest = %q, want %q", got, "v0.4.1")
	}
}

func TestLatestDoesNotFollowTheRedirect(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Location", "/releases/tag/v0.4.1")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	useBase(t, srv.URL)

	if _, err := Latest(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if hits != 1 {
		t.Errorf("Latest made %d requests, want exactly 1: the redirect must not be followed", hits)
	}
}

func TestLatestSendsHoardsUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Header().Set("Location", "/releases/tag/v0.4.1")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	useBase(t, srv.URL)

	if _, err := Latest(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !strings.HasPrefix(ua, "hoard/") {
		t.Errorf("User-Agent = %q, want it to start with %q", ua, "hoard/")
	}
}

func TestLatestWithoutALocationHeaderIsAnError(t *testing.T) {
	srv := redirectingTo(t, "")
	useBase(t, srv.URL)

	got, err := Latest(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatalf("Latest = %q with no error; a response carrying no Location must be an error", got)
	}
}

func TestLatestWithAnUnparseableLocationIsAnError(t *testing.T) {
	srv := redirectingTo(t, "https://github.com/spiffcs/hoard/releases")
	useBase(t, srv.URL)

	got, err := Latest(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatalf("Latest = %q with no error; a Location naming no tag must be an error", got)
	}
}
