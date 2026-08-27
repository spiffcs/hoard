package pricing

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestResolveSeparatesActivityFromWarnings(t *testing.T) {
	s := newStore(t)
	for _, c := range []scryfall.Card{
		{ID: "real-id", Set: "m3c", CollectorNumber: "218", Name: "Acidic Slime",
			PriceUSD: f64(0.34), ScryfallURL: "http://x"},
		{ID: "token-id", Set: "tc20", CollectorNumber: "1", Name: "Soldier",
			PriceUSD: f64(0.05), ScryfallURL: "http://y"},
	} {
		if err := s.AddCardFinish(c, finish.Nonfoil, 1); err != nil {
			t.Fatalf("AddCard: %v", err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "TC20") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"data": {"cards": [
			{"uuid": "uuid-real", "identifiers": {"scryfallId": "real-id"}}
		]}}`))
		zw.Close()
	}))
	defer srv.Close()

	var activity []string
	var warnings, groups []string
	f := New(s, t.TempDir()).WithBaseURL(srv.URL).
		WithProgress(func(msg string) { activity = append(activity, msg) }).
		WithWarning(func(msg, group string) {
			warnings = append(warnings, msg)
			groups = append(groups, group)
		})

	if _, err := f.resolve(context.Background(), []Ref{
		{ScryfallID: "real-id", SetCode: "m3c"},
		{ScryfallID: "token-id", SetCode: "tc20"},
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	joined := strings.Join(activity, "\n")
	if !strings.Contains(joined, "resolving card ids · set") {
		t.Errorf("per-set progress did not arrive as transient activity: %q", joined)
	}
	if strings.Contains(joined, "once only") {
		t.Errorf("the activity message still carries the \"once only\" aside, "+
			"which clutters the step line: %q", joined)
	}
	if strings.Contains(joined, "skipping set") {
		t.Errorf("a skipped set was reported as transient activity, so it will "+
			"vanish instead of being counted: %q", joined)
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly the one skipped set", warnings)
	}
	if !strings.Contains(warnings[0], "skipping set tc20") {
		t.Errorf("warning = %q, want the skipped set", warnings[0])
	}
	if groups[0] != "sets are not in MTGJSON, so their printings are unpriced" {
		t.Errorf("group = %q, want the label the summary line is built from", groups[0])
	}
}
