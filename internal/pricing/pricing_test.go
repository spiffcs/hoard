package pricing

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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

// A card Scryfall cannot price for a finish you hold is a gap.
func unpricedFoil() scryfall.Card {
	return scryfall.Card{
		ID: "ripple-id", Set: "m3c", CollectorNumber: "218", Name: "Acidic Slime",
		PriceUSD: f64(0.34), ScryfallURL: "http://x",
	}
}

// With no gaps there is nothing to fetch, so the 50 MB scan must not happen —
// which this proves by pointing the fetcher at a cache dir it would fail on.
func TestFillGapsDoesNothingWithoutGaps(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(scryfall.Card{
		ID: "u", Set: "uma", CollectorNumber: "7", Name: "Ulamog",
		PriceUSD: f64(10), PriceUSDFoil: f64(25), ScryfallURL: "http://x",
	}, "nonfoil", 1); err != nil {
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

// Once every gap has been asked about recently, the scan is skipped entirely.
// That is the difference between a 1.7s refresh and a 0.2s one.
func TestFillGapsSkipsWhenEveryGapWasAskedRecently(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil || len(gaps) != 1 {
		t.Fatalf("gaps = %+v, %v", gaps, err)
	}
	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatalf("RecordPriceGapChecks: %v", err)
	}

	// A cache dir that does not exist and no network: reaching MTGJSON at all
	// would fail, so a clean skip is the only way this passes.
	report, err := New(s, filepath.Join(t.TempDir(), "nope")).FillGaps(context.Background())
	if err != nil {
		t.Fatalf("FillGaps: %v", err)
	}
	if !report.Skipped || report.Gaps != 1 {
		t.Errorf("report = %+v, want the scan skipped", report)
	}
}

// Refs already carrying an id need no set-file download once the vendor
// links are stamped too, which is what makes a collection-wide read free
// after the first run.
func TestResolvableUsesStoredIDsWithoutFetching(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	// The link pass has already happened for this card (an empty record
	// counts: asked-and-none must not refetch).
	if err := s.SaveCardKingdomLinks(map[string]store.CKLinks{"ripple-id": {}}); err != nil {
		t.Fatalf("SaveCardKingdomLinks: %v", err)
	}
	// Unreachable cache and no network; only the supplied id can satisfy this.
	byUUID, _, err := New(s, filepath.Join(t.TempDir(), "nope")).want(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "known-uuid"}})
	if err != nil {
		t.Fatalf("want: %v", err)
	}
	if len(byUUID) != 1 {
		t.Errorf("resolvable = %d, want the supplied id counted", len(byUUID))
	}
}

// A card whose uuid is known but whose vendor links were never asked about
// fetches its set file once — the pre-v15 backfill — stamps the links
// (present or recorded-absent), and never fetches again.
func TestResolveHarvestsCardKingdomLinksOnce(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "nonfoil", 1); err != nil {
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

	// Byte progress stays silent during the per-set pass: dozens of tiny
	// bars filling and vanishing read as one download failing repeatedly.
	var byteEvents int
	f := New(s, t.TempDir()).WithBaseURL(srv.URL).
		WithBytes(func(done, total int64) { byteEvents++ })
	if _, _, err := f.want(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "uuid-ripple"}}); err != nil {
		t.Fatalf("want: %v", err)
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
	if d.CKURL == nil || *d.CKURL != "https://mtgjson.com/links/aa" ||
		d.CKFoilURL == nil || *d.CKFoilURL != "https://mtgjson.com/links/bb" {
		t.Fatalf("links = %v/%v, want both stamped", d.CKURL, d.CKFoilURL)
	}

	// Stamped means done: a second pass fetches nothing (the cache dir is
	// fresh, so a fetch would hit the server again).
	f2 := New(s, t.TempDir()).WithBaseURL(srv.URL)
	if _, _, err := f2.want(context.Background(),
		[]Ref{{ScryfallID: "ripple-id", SetCode: "m3c", MTGJSONUUID: "uuid-ripple"}}); err != nil {
		t.Fatalf("second want: %v", err)
	}
	if hits != 1 {
		t.Errorf("set file fetched %d times after stamping, want still 1", hits)
	}
}

// Progress is reported through a callback so this package prints nothing itself.
func TestProgressIsOptional(t *testing.T) {
	s := newStore(t)
	fetcher := New(s, t.TempDir())
	fetcher.say("silent %d", 1) // must not panic with no reporter

	var got []string
	fetcher.WithProgress(func(m string) { got = append(got, m) })
	fetcher.say("hello %s", "world")
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("progress = %v", got)
	}
}
