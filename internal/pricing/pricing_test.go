package pricing

import (
	"context"
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
	}, "normal", 1); err != nil {
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

// Refs already carrying an id need no set-file download, which is what makes a
// collection-wide read free after the first run.
func TestResolvableUsesStoredIDsWithoutFetching(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "normal", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
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
