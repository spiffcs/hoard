package scryfall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func schedulePlan(endpoints ...string) []time.Time {
	p := pacer{next: map[string]time.Time{}}
	base := time.Unix(1700000000, 0)
	out := make([]time.Time, len(endpoints))
	for i, e := range endpoints {
		out[i] = p.schedule(base, e)
	}
	return out
}

const (
	namedPath = "https://api.scryfall.com/cards/named?fuzzy=sol+ring"
	autoPath  = "https://api.scryfall.com/cards/autocomplete?q=sol"
)

func TestPacerBoundsTheAggregateRequestRate(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 60*time.Millisecond, 20*time.Millisecond
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	var endpoints []string
	for range 3 {
		endpoints = append(endpoints, namedPath, autoPath)
	}
	at := schedulePlan(endpoints...)

	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < defaultGap {
			t.Errorf("requests %d and %d are scheduled %v apart; no two API requests "+
				"may be closer than %v whatever their endpoint", i-1, i, gap, defaultGap)
		}
	}
}

func TestPacerKeepsSlowEndpointsApartFromEachOther(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 60*time.Millisecond, 20*time.Millisecond
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	at := schedulePlan(namedPath, autoPath, namedPath, autoPath, namedPath)

	for _, i := range []int{2, 4} {
		if gap := at[i].Sub(at[i-2]); gap < slowGap {
			t.Errorf("two %s requests are scheduled %v apart; a slow endpoint wants %v",
				"/cards/named", gap, slowGap)
		}
	}
}

func TestPacerNeverSchedulesBeforeTheCallerIsReady(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 60*time.Millisecond, 20*time.Millisecond
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	p := pacer{next: map[string]time.Time{}}
	base := time.Unix(1700000000, 0)

	if at := p.schedule(base, autoPath); !at.Equal(base) {
		t.Errorf("first request scheduled at %v, want no wait at all", at.Sub(base))
	}

	late := base.Add(time.Hour)
	if at := p.schedule(late, autoPath); !at.Equal(late) {
		t.Errorf("a request an hour later waited %v; a quiet pacer holds nobody up",
			at.Sub(late))
	}
}

func TestRequestsGoThroughThePacer(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 0, 0
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	apiPacer = pacer{next: map[string]time.Time{}}
	defer func() { apiPacer = pacer{next: map[string]time.Time{}} }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	ctx := context.Background()
	if _, err := NamedFuzzy(ctx, "sol ring"); err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if _, err := Autocomplete(ctx, "sol"); err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}

	apiPacer.mu.Lock()
	defer apiPacer.mu.Unlock()
	for _, class := range []string{"/cards/named", ""} {
		if _, seen := apiPacer.next[class]; !seen {
			t.Errorf("the pacer never saw a %q request; apiDo went around it",
				orDefaultClass(class))
		}
	}
}

func orDefaultClass(class string) string {
	if class == "" {
		return "default-class"
	}
	return class
}
