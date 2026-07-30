package scryfall

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestParseCardURL(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantSet    string
		wantNumber string
		wantErr    bool
	}{
		{
			name:       "canonical with slug",
			raw:        "https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:       "without slug",
			raw:        "https://scryfall.com/card/uma/7",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:       "www host",
			raw:        "https://www.scryfall.com/card/neo/123/kaito",
			wantSet:    "neo",
			wantNumber: "123",
		},
		{
			name:       "letter in collector number",
			raw:        "https://scryfall.com/card/sld/1234a/some-card",
			wantSet:    "sld",
			wantNumber: "1234a",
		},
		{
			name:       "surrounding whitespace",
			raw:        "  https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre  ",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:    "wrong host",
			raw:     "https://example.com/card/uma/7/ulamog",
			wantErr: true,
		},
		{
			name:    "not a card path",
			raw:     "https://scryfall.com/sets/uma",
			wantErr: true,
		},
		{
			name:    "missing number",
			raw:     "https://scryfall.com/card/uma",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, number, err := ParseCardURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got set=%q number=%q", set, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if set != tt.wantSet || number != tt.wantNumber {
				t.Fatalf("got set=%q number=%q, want set=%q number=%q", set, number, tt.wantSet, tt.wantNumber)
			}
		})
	}
}

func TestParsePrice(t *testing.T) {
	if p := parsePrice(""); p != nil {
		t.Errorf("empty string: want nil, got %v", *p)
	}
	if p := parsePrice("not-a-number"); p != nil {
		t.Errorf("garbage: want nil, got %v", *p)
	}
	p := parsePrice("3.49")
	if p == nil || *p != 3.49 {
		t.Errorf("valid price: want 3.49, got %v", p)
	}
}

func TestIdentifierMarshalOmitsEmpty(t *testing.T) {
	b, err := json.Marshal(Identifier{ID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"id":"abc"}` {
		t.Errorf("id identifier: got %s", got)
	}
	b, _ = json.Marshal(Identifier{Set: "uma", CollectorNumber: "7"})
	if got := string(b); got != `{"set":"uma","collector_number":"7"}` {
		t.Errorf("set/num identifier: got %s", got)
	}
	b, _ = json.Marshal(Identifier{Name: "Sol Ring"})
	if got := string(b); got != `{"name":"Sol Ring"}` {
		t.Errorf("name identifier: got %s", got)
	}
}

func TestAutocomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "Ulamog" {
			t.Errorf("q = %q, want Ulamog", got)
		}
		w.Write([]byte(`{"object":"catalog","total_values":2,"data":["Ulamog, the Infinite Gyre","Ulamog, the Ceaseless Hunger"]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	names, err := Autocomplete(context.Background(), "Ulamog")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if len(names) != 2 || names[0] != "Ulamog, the Infinite Gyre" {
		t.Errorf("names = %v", names)
	}
}

func TestSearchPrintsPaginatesAndDecodes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			// First page: exact-name query present, has_more true.
			if q := r.URL.Query().Get("q"); q != `!"Sol Ring" game:paper` {
				t.Errorf("q = %q", q)
			}
			w.Write([]byte(`{"object":"list","has_more":true,"next_page":"` + srv.URL + `/cards/search?page=2",
				"data":[{"id":"a","name":"Sol Ring","set":"c21","set_name":"Commander 2021","collector_number":"1",
				"finishes":["nonfoil","foil"],"prices":{"usd":"2.00","usd_foil":"5.00"}}]}`))
			return
		}
		// Second page: no more.
		w.Write([]byte(`{"object":"list","has_more":false,
			"data":[{"id":"b","name":"Sol Ring","set":"mps","set_name":"Masterpiece","collector_number":"1",
			"finishes":["foil"],"prices":{"usd":null,"usd_foil":"120.00"}}]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	cards, err := SearchPrints(context.Background(), "Sol Ring")
	if err != nil {
		t.Fatalf("SearchPrints: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2 (paginated)", len(cards))
	}
	if cards[0].SetName != "Commander 2021" || len(cards[0].Finishes) != 2 {
		t.Errorf("card[0] fields wrong: %+v", cards[0])
	}
	if cards[1].PriceUSD != nil {
		t.Errorf("card[1] usd should be nil")
	}
	if cards[1].PriceUSDFoil == nil || *cards[1].PriceUSDFoil != 120.0 {
		t.Errorf("card[1] foil price wrong: %+v", cards[1].PriceUSDFoil)
	}
}

func TestNamedFuzzy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("fuzzy")
		if q == "Ulomog infinte" { // noisy OCR input still resolves
			w.Write([]byte(`{"object":"card","id":"u1","name":"Ulamog, the Infinite Gyre","set":"uma","collector_number":"7"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"object":"error","status":404,"details":"too ambiguous"}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	card, err := NamedFuzzy(context.Background(), "Ulomog infinte")
	if err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if card == nil || card.Name != "Ulamog, the Infinite Gyre" {
		t.Fatalf("fuzzy match wrong: %+v", card)
	}

	none, err := NamedFuzzy(context.Background(), "zzz")
	if err != nil {
		t.Fatalf("NamedFuzzy no-match returned error: %v", err)
	}
	if none != nil {
		t.Errorf("want nil for ambiguous/no match, got %+v", none)
	}
}

func TestSearchPrintsNoMatchReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"object":"error","status":404,"details":"No cards found"}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	cards, err := SearchPrints(context.Background(), "Nonexistent Card Xyz")
	if err != nil {
		t.Fatalf("SearchPrints no-match returned error: %v", err)
	}
	if cards != nil {
		t.Errorf("want nil cards, got %v", cards)
	}
}

func TestEtchedFallbackPrice(t *testing.T) {
	ac := apiCard{ID: "x", Name: "Etched Card"}
	ac.Prices.USDFoil = ""
	ac.Prices.USDEtched = "42.00"
	c := ac.toCard(nil)
	if c.PriceUSDFoil == nil || *c.PriceUSDFoil != 42.0 {
		t.Errorf("etched fallback: want 42, got %v", c.PriceUSDFoil)
	}
}

func TestFetchCollectionChunksAndAggregates(t *testing.T) {
	// Build 80 identifiers so the client must split into two chunks (75 + 5).
	ids := make([]Identifier, 80)
	for i := range ids {
		ids[i] = Identifier{Name: "Card " + strconv.Itoa(i)}
	}

	var chunkSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Identifiers []Identifier `json:"identifiers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("server decode: %v", err)
		}
		chunkSizes = append(chunkSizes, len(req.Identifiers))
		// Echo back one found card per identifier; mark the very first as not found.
		var resp struct {
			Object   string       `json:"object"`
			Data     []apiCard    `json:"data"`
			NotFound []Identifier `json:"not_found"`
		}
		resp.Object = "list"
		for i, id := range req.Identifiers {
			if len(chunkSizes) == 1 && i == 0 {
				resp.NotFound = append(resp.NotFound, id)
				continue
			}
			resp.Data = append(resp.Data, apiCard{ID: "x", Name: id.Name})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Point the client at the test server for the duration of the test.
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	found, notFound, err := FetchCollection(context.Background(), ids)
	if err != nil {
		t.Fatalf("FetchCollection: %v", err)
	}
	if len(chunkSizes) != 2 || chunkSizes[0] != 75 || chunkSizes[1] != 5 {
		t.Errorf("chunk sizes = %v, want [75 5]", chunkSizes)
	}
	if len(found) != 79 {
		t.Errorf("found = %d, want 79", len(found))
	}
	if len(notFound) != 1 {
		t.Errorf("notFound = %d, want 1", len(notFound))
	}
}

// The store keeps whatever Scryfall sends so that fields this package does not
// name are still available later. If Raw were dropped — or rebuilt by
// re-marshalling the decoded struct — every unnamed field would vanish, and the
// loss would only show up much later as an empty column.
func TestRawCarriesUnmodelledFields(t *testing.T) {
	const card = `{"id":"raw-id","name":"Bitterblossom","set":"uma",
	  "collector_number":"85","scryfall_uri":"http://x","prices":{"usd":"34.11"},
	  "rarity":"mythic","type_line":"Tribal Enchantment — Faerie","cmc":2.0,
	  "artist":"Jesper Ejsing","color_identity":["B"]}`

	check := func(t *testing.T, c Card) {
		t.Helper()
		if len(c.Raw) == 0 {
			t.Fatal("Raw is empty; the response bytes were dropped")
		}
		var got map[string]any
		if err := json.Unmarshal(c.Raw, &got); err != nil {
			t.Fatalf("Raw is not valid JSON: %v", err)
		}
		// None of these are fields on Card, which is the point.
		for k, want := range map[string]any{
			"rarity": "mythic", "artist": "Jesper Ejsing", "cmc": 2.0,
		} {
			if got[k] != want {
				t.Errorf("Raw[%q] = %v, want %v", k, got[k], want)
			}
		}
	}

	t.Run("FetchCard", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(card))
		}))
		defer srv.Close()
		old := apiBase
		apiBase = srv.URL
		defer func() { apiBase = old }()

		c, err := FetchCard(context.Background(), "uma", "85")
		if err != nil {
			t.Fatalf("FetchCard: %v", err)
		}
		check(t, *c)
	})

	// The list endpoints are the ones that must decode in two passes; a single
	// pass straight to apiCard is what silently loses the bytes.
	t.Run("FetchCollection", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data":[` + card + `],"not_found":[]}`))
		}))
		defer srv.Close()
		old := apiBase
		apiBase = srv.URL
		defer func() { apiBase = old }()

		found, _, err := FetchCollection(context.Background(), []Identifier{{ID: "raw-id"}})
		if err != nil || len(found) != 1 {
			t.Fatalf("FetchCollection: %v, got %d cards", err, len(found))
		}
		check(t, found[0])
	})

	t.Run("SearchPrints", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data":[` + card + `],"has_more":false}`))
		}))
		defer srv.Close()
		old := apiBase
		apiBase = srv.URL
		defer func() { apiBase = old }()

		cards, err := SearchPrints(context.Background(), "Bitterblossom")
		if err != nil || len(cards) != 1 {
			t.Fatalf("SearchPrints: %v, got %d cards", err, len(cards))
		}
		check(t, cards[0])
	})
}

// A 429 on any one chunk used to discard every card fetched before it, so the
// longer the collection the more work a single throttle threw away. Scryfall's
// budget is wider than one process, so waiting is the right response.
func TestFetchCollectionWaitsOutRateLimiting(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"object":"error","status":429,"details":"You are being rate-limited"}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"a","name":"Sol Ring","set":"c21",
			"collector_number":"1","prices":{"usd":"2.00"}}],"not_found":[]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	found, _, err := FetchCollection(context.Background(), []Identifier{{ID: "a"}})
	if err != nil {
		t.Fatalf("FetchCollection: %v", err)
	}
	if len(found) != 1 || found[0].Name != "Sol Ring" {
		t.Errorf("found = %+v, want the card from the retried request", found)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2 (one limited, one retried)", calls)
	}
}

// A rate-limit wait can be a minute; it must still answer ctrl-c rather than
// pinning the terminal until Scryfall relents.
func TestFetchCollectionRateLimitWaitIsCancellable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already given up before the wait begins

	done := make(chan error, 1)
	go func() {
		_, _, err := FetchCollection(ctx, []Identifier{{ID: "a"}})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting after cancellation; the sleep ignores the context")
	}
}

func TestRetryAfterHeader(t *testing.T) {
	mk := func(v string) *http.Response {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		return &http.Response{Header: h}
	}
	fallback := 30 * time.Second
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"5", 5 * time.Second},
		{" 12 ", 12 * time.Second},
		{"", fallback},
		{"not-a-number", fallback},
		{"0", fallback},
		// A surprising header must not park the process for an afternoon.
		{"86400", 90 * time.Second},
	} {
		if got := retryAfter(mk(tc.header), fallback); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}
