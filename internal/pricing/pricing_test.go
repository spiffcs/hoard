package pricing

import (
	"compress/gzip"
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func f64(v float64) *float64 { return &v }

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func unpricedFoil() scryfall.Card {
	return scryfall.Card{
		ID: "ripple-id", Set: "m3c", CollectorNumber: "218", Name: "Acidic Slime",
		PriceUSD: f64(0.34), ScryfallURL: "http://x",
	}
}

func TestFillGapsDoesNothingWithoutGaps(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(scryfall.Card{
		ID: "u", Set: "uma", CollectorNumber: "7", Name: "Ulamog",
		PriceUSD: f64(10), PriceUSDFoil: f64(25), ScryfallURL: "http://x",
	}, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	report, err := New(s, t.TempDir()).FillGaps(context.Background())
	if err != nil {
		t.Fatalf("FillGaps: %v", err)
	}
	if report.Gaps != 0 || report.Skipped {
		t.Errorf("report = %+v, want an empty pass", report)
	}
}

func TestFillGapsSkipsWhenEveryGapWasAskedRecently(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil || len(gaps) != 1 {
		t.Fatalf("gaps = %+v, %v", gaps, err)
	}
	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatalf("RecordPriceGapChecks: %v", err)
	}

	report, err := New(s, filepath.Join(t.TempDir(), "nope")).FillGaps(context.Background())
	if err != nil {
		t.Fatalf("FillGaps: %v", err)
	}
	if !report.Skipped || report.Gaps != 1 {
		t.Errorf("report = %+v, want the scan skipped", report)
	}
}

func TestResolvableUsesStoredIDsWithoutFetching(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	if err := s.SaveCardKingdomLinks(map[string]store.CKLinks{"ripple-id": {}}); err != nil {
		t.Fatalf("SaveCardKingdomLinks: %v", err)
	}

	f := New(s, filepath.Join(t.TempDir(), "nope"))
	refs := []Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "known-uuid"}}
	uuids, err := f.resolve(context.Background(), refs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	byUUID, _ := f.want(refs, uuids)
	if len(byUUID) != 1 {
		t.Errorf("resolvable = %d, want the supplied id counted", len(byUUID))
	}
}

func TestResolveHarvestsCardKingdomLinksOnce(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"data": {"cards": [
			{"uuid": "uuid-ripple", "identifiers": {"scryfallId": "ripple-id"},
			 "purchaseUrls": {"cardKingdom": "https://mtgjson.com/links/aa",
			                  "cardKingdomFoil": "https://mtgjson.com/links/bb"}}
		]}}`))
		zw.Close()
	}))
	defer srv.Close()

	var byteEvents int
	f := New(s, t.TempDir()).WithBaseURL(srv.URL).
		WithBytes(func(done, total int64) { byteEvents++ })
	if _, err := f.resolve(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "uuid-ripple"}}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if hits != 1 {
		t.Fatalf("set file fetched %d times, want once", hits)
	}
	if byteEvents != 0 {
		t.Errorf("set-file download reported %d byte events, want none", byteEvents)
	}
	d, err := s.CardDetail("ripple-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.CKURL != "https://mtgjson.com/links/aa" ||
		d.CKFoilURL != "https://mtgjson.com/links/bb" {
		t.Fatalf("links = %q/%q, want both stamped", d.CKURL, d.CKFoilURL)
	}

	f2 := New(s, t.TempDir()).WithBaseURL(srv.URL)
	if _, err := f2.resolve(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "uuid-ripple"}}); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if hits != 1 {
		t.Errorf("set file fetched %d times after stamping, want still 1", hits)
	}
}

func TestProgressIsOptional(t *testing.T) {
	s := newStore(t)
	fetcher := New(s, t.TempDir())
	fetcher.say("silent %d", 1)

	var got []string
	fetcher.WithProgress(func(m string) { got = append(got, m) })
	fetcher.say("hello %s", "world")
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("progress = %v", got)
	}
}

func TestFillGapsRefreshesFallbackPrices(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if err := s.UpsertAltPrices([]store.AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "uuid-ripple",
		PriceUSDFoil: f64(74.99), SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}

	if err := s.SaveMTGJSONUUIDs(map[string]string{"ripple-id": "uuid-ripple"}); err != nil {
		t.Fatalf("SaveMTGJSONUUIDs: %v", err)
	}
	if err := s.SaveCardKingdomLinks(map[string]store.CKLinks{"ripple-id": {}}); err != nil {
		t.Fatalf("SaveCardKingdomLinks: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"meta": {"date": "2026-08-02", "version": "5"},
 "data": {"uuid-ripple": {"paper": {
   "manapool":    {"currency": "USD", "retail": {"foil": {"2026-08-02": 38.55}}},
   "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-08-02": 74.99}}}
 }}}}`))
		zw.Close()
	}))
	defer srv.Close()

	rep, err := New(s, t.TempDir()).WithBaseURL(srv.URL).FillGaps(context.Background())
	if err != nil {
		t.Fatalf("FillGaps: %v", err)
	}
	if rep.Skipped || rep.Gaps != 0 {
		t.Errorf("report = %+v, want an unskipped pass with no true gaps", rep)
	}
	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Price() == nil || *rows[0].Price() != 38.55 ||
		rows[0].AltSource != "manapool" {
		t.Fatalf("rows = %+v, want the fallback refreshed to manapool's 38.55", rows)
	}
}

func TestPricesOverlayTreatedFoil(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	gz := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			zw := gzip.NewWriter(w)
			zw.Write([]byte(body))
			zw.Close()
		}
	}
	plain := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) { w.Write([]byte(body)) }
	}
	routes := map[string]func(w http.ResponseWriter){
		"/M3C.json.gz": gz(`{"data": {"cards": [
			{"uuid": "uuid-akroma", "identifiers": {"scryfallId": "ripple-id",
			 "tcgplayerAlternativeFoilProductId": "553005"}}
		]}}`),
		"/AllPricesToday.json.gz": gz(`{"meta": {"date": "2026-08-01"}, "data": {
			"uuid-akroma": {"paper": {
			  "tcgplayer":   {"currency": "USD", "retail": {"normal": {"2026-08-01": 14.87}}},
			  "cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-08-01": 21.99}}},
			  "manapool":    {"currency": "USD", "retail": {"foil": {"2026-08-01": 21.78}}}
			}}}}`),
		"/tcgplayer/1/groups": plain(`{"results": [
			{"groupId": 23445, "name": "Commander: Modern Horizons 3", "abbreviation": "M3C"}]}`),
		"/tcgplayer/1/23445/prices": plain(`{"results": [
			{"productId": 552925, "marketPrice": 15.33, "subTypeName": "Normal"},
			{"productId": 553005, "marketPrice": 17.56, "subTypeName": "Foil"}]}`),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected fetch: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w)
	}))
	defer srv.Close()

	f := New(s, t.TempDir()).WithBaseURL(srv.URL).WithTCGCSVBaseURL(srv.URL)
	got, err := f.Prices(context.Background(), []Ref{{ScryfallID: "ripple-id", SetCode: "m3c"}})
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	p := got["ripple-id"]
	if p.Foil == nil || *p.Foil != 17.56 || p.FoilSource != "tcgplayer" {
		t.Fatalf("price = %+v, want the treated product's tcgplayer figure", p)
	}
	if p.USD == nil || *p.USD != 14.87 {
		t.Errorf("normal = %+v, want the feed's own figure untouched", p)
	}

	ids, _, stamped, err := s.TCGAltProducts()
	if err != nil {
		t.Fatalf("TCGAltProducts: %v", err)
	}
	if ids["ripple-id"] != "553005" || !stamped["ripple-id"] {
		t.Errorf("stored ids = %v stamped %v, want the product learned once", ids, stamped)
	}
}

func TestPricesOverlaySoftFailure(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/M3C.json.gz":
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"data": {"cards": [
				{"uuid": "uuid-akroma", "identifiers": {"scryfallId": "ripple-id",
				 "tcgplayerAlternativeFoilProductId": "553005"}}]}}`))
			zw.Close()
		case "/AllPricesToday.json.gz":
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"data": {"uuid-akroma": {"paper": {
				"manapool": {"currency": "USD", "retail": {"foil": {"2026-08-01": 21.78}}}}}}}`))
			zw.Close()
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	f := New(s, t.TempDir()).WithBaseURL(srv.URL).WithTCGCSVBaseURL(srv.URL)
	got, err := f.Prices(context.Background(), []Ref{{ScryfallID: "ripple-id", SetCode: "m3c"}})
	if err != nil {
		t.Fatalf("Prices must not fail with tcgcsv down: %v", err)
	}
	if p := got["ripple-id"]; p.Foil == nil || *p.Foil != 21.78 || p.FoilSource != "manapool" {
		t.Errorf("price = %+v, want the feed's fallback answer", p)
	}
}

func TestRefreshQuotesBypassesTheDayCache(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	gz := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			zw := gzip.NewWriter(w)
			zw.Write([]byte(body))
			zw.Close()
		}
	}
	plain := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) { w.Write([]byte(body)) }
	}
	routes := map[string]func(w http.ResponseWriter){
		"/M3C.json.gz": gz(`{"data": {"cards": [
			{"uuid": "uuid-akroma", "identifiers": {"scryfallId": "ripple-id",
			 "tcgplayerAlternativeFoilProductId": "553005"}}]}}`),
		"/AllPricesToday.json.gz": gz(`{"data": {"uuid-akroma": {"paper": {
			"manapool": {"currency": "USD", "retail": {"foil": {"2026-08-01": 21.78}}},
			"cardkingdom": {"currency": "USD", "retail": {"foil": {"2026-08-01": 21.99}}}}}}}`),
		"/tcgplayer/1/groups": plain(`{"results": [{"groupId": 23445, "abbreviation": "M3C"}]}`),
		"/tcgplayer/1/23445/prices": plain(`{"results": [
			{"productId": 553005, "marketPrice": 17.56, "subTypeName": "Foil"}]}`),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := New(s, t.TempDir()).WithBaseURL(srv.URL).WithTCGCSVBaseURL(srv.URL)
	refs := []Ref{{ScryfallID: "ripple-id", SetCode: "m3c"}}
	hasTCGFoil := func(qs map[string][]mtgjson.Quote) bool {
		for _, q := range qs["ripple-id"] {
			if q.Provider == "tcgplayer" && q.Finish == finish.Foil && q.Price == 17.56 {
				return true
			}
		}
		return false
	}

	f.saveQuotes(refs, map[string][]mtgjson.Quote{"ripple-id": {
		{Provider: "manapool", Kind: mtgjson.Retail, Finish: finish.Foil, Price: 21.78}}})
	qs, err := f.Quotes(context.Background(), refs)
	if err != nil {
		t.Fatalf("Quotes: %v", err)
	}
	if hasTCGFoil(qs) {
		t.Fatal("the day-cache path must serve the stale bundle — that is what makes it a cache")
	}

	qs, err = f.RefreshQuotes(context.Background(), refs)
	if err != nil {
		t.Fatalf("RefreshQuotes: %v", err)
	}
	if !hasTCGFoil(qs) {
		t.Fatalf("refreshed quotes = %+v, want the overlay's tcgplayer foil quote", qs["ripple-id"])
	}
	cached, ok := f.CachedQuotes(refs)
	if !ok || !hasTCGFoil(cached) {
		t.Errorf("cached after refresh = ok %v %+v, want the fresh bundle saved back", ok, cached["ripple-id"])
	}
}

func TestHistoryOverlaySweepsOnlyHeldFinishes(t *testing.T) {
	gz := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			zw := gzip.NewWriter(w)
			zw.Write([]byte(body))
			zw.Close()
		}
	}
	plain := func(body string) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) { w.Write([]byte(body)) }
	}
	routes := map[string]func(w http.ResponseWriter){
		"/M3C.json.gz": gz(`{"data": {"cards": [
			{"uuid": "uuid-akroma", "identifiers": {"scryfallId": "ripple-id",
			 "tcgplayerAlternativeFoilProductId": "553005"}}
		]}}`),
		"/AllPrices.json.gz": gz(`{"meta": {"date": "2026-08-01"}, "data": {
			"uuid-akroma": {"paper": {
			  "tcgplayer": {"currency": "USD", "retail": {"normal": {"2026-08-01": 14.87}}}
			}}}}`),
		"/tcgplayer/1/groups": plain(`{"results": [
			{"groupId": 23445, "name": "Commander: Modern Horizons 3", "abbreviation": "M3C"}]}`),
		"/tcgplayer/1/23445/prices": plain(`{"results": [
			{"productId": 553005, "marketPrice": 17.56, "subTypeName": "Foil"}]}`),
	}

	const days = 4
	sweep := func(t *testing.T, fin finish.Finish) int {
		t.Helper()
		s := newStore(t)
		if err := s.AddCardFinish(unpricedFoil(), fin, 1); err != nil {
			t.Fatalf("AddCard: %v", err)
		}
		var archives int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/archive/tcgplayer/") {
				archives++
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h, ok := routes[r.URL.Path]
			if !ok {
				t.Errorf("unexpected fetch: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h(w)
		}))
		defer srv.Close()

		f := New(s, t.TempDir()).WithBaseURL(srv.URL).WithTCGCSVBaseURL(srv.URL)
		if _, _, err := f.History(context.Background(),
			[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", Finish: fin}}, days); err != nil {
			t.Fatalf("History: %v", err)
		}
		return archives
	}

	if n := sweep(t, finish.Nonfoil); n != 0 {
		t.Errorf("a non-foil holding pulled %d treated-foil archives; want none", n)
	}
	if n := sweep(t, finish.Foil); n != days-1 {
		t.Errorf("a foil holding pulled %d treated-foil archives; want %d", n, days-1)
	}
}

func TestHistoryOverlaySweepsEveryFinishWhenRefSaysNothing(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	var archives int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/archive/tcgplayer/"):
			archives++
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/M3C.json.gz":
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"data": {"cards": [
				{"uuid": "uuid-akroma", "identifiers": {"scryfallId": "ripple-id",
				 "tcgplayerAlternativeFoilProductId": "553005"}}]}}`))
			zw.Close()
		case r.URL.Path == "/AllPrices.json.gz":
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"data": {"uuid-akroma": {"paper": {
				"tcgplayer": {"currency": "USD", "retail": {"normal": {"2026-08-01": 14.87}}}}}}}`))
			zw.Close()
		case r.URL.Path == "/tcgplayer/1/groups":
			w.Write([]byte(`{"results": [{"groupId": 23445, "abbreviation": "M3C"}]}`))
		default:
			w.Write([]byte(`{"results": [{"productId": 553005, "marketPrice": 17.56, "subTypeName": "Foil"}]}`))
		}
	}))
	defer srv.Close()

	f := New(s, t.TempDir()).WithBaseURL(srv.URL).WithTCGCSVBaseURL(srv.URL)
	if _, _, err := f.History(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c"}}, 4); err != nil {
		t.Fatalf("History: %v", err)
	}
	if archives != 3 {
		t.Errorf("a finishless ref pulled %d treated-foil archives; want 3", archives)
	}
}
