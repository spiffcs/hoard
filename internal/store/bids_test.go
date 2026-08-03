package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

// BidSeries reads only the bid table, whole series, oldest first — the
// buylist twin of PriceSeries.
func TestBidSeriesReturnsTheWholeSeries(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO card_bid_history VALUES ('ulamog-id','nonfoil',4.0,'cardkingdom','2026-07-02T00:00:00Z')`,
		`INSERT INTO card_bid_history VALUES ('ulamog-id','nonfoil',5.0,'cardkingdom','2026-07-09T00:00:00Z')`,
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',10.0,'scryfall','2026-07-09T05:00:00Z')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := s.BidSeries("ulamog-id", "nonfoil")
	if err != nil {
		t.Fatalf("BidSeries: %v", err)
	}
	if len(got) != 2 || got[0].Price != 4.0 || got[1].Price != 5.0 {
		t.Errorf("series = %+v, want the two bids oldest first", got)
	}
	// The retail row must not leak into the bid series.
	for _, p := range got {
		if p.Price == 10.0 {
			t.Errorf("retail observation leaked into the bid series: %+v", p)
		}
	}
}

// The bid backfill is bounded by the bid table's own live era — months of
// retail history must not stop a bid import, and a live bid row must.
func TestBackfillBidsBoundsAgainstOwnFirstObservations(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	// Retail history reaching back to May; bid history starting July 10.
	for _, stmt := range []string{
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',10.0,'scryfall','2026-05-01T00:00:00Z')`,
		`INSERT INTO card_bid_history VALUES ('ulamog-id','nonfoil',6.0,'cardkingdom','2026-07-10T05:00:00Z')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	inserted, cards, err := s.BackfillBids(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-08", "normal", 4.00), // before the bid era: imports
			obs("2026-07-09", "normal", 5.00),
			obs("2026-07-10", "normal", 5.90), // the bid era itself: excluded
		},
	})
	if err != nil {
		t.Fatalf("BackfillBids: %v", err)
	}
	if inserted != 2 || cards != 1 {
		t.Errorf("inserted %d across %d cards, want the two pre-era days", inserted, cards)
	}
	got, _ := s.BidSeries("ulamog-id", "nonfoil")
	want := []float64{4.00, 5.00, 6.00}
	var prices []float64
	for _, p := range got {
		prices = append(prices, p.Price)
	}
	if !slices.Equal(prices, want) {
		t.Errorf("series = %v, want %v (translated to nonfoil, live row last)", prices, want)
	}
	// The retail table is untouched by a bid backfill.
	retail, _ := s.PriceSeries("ulamog-id", "nonfoil")
	if len(retail) != 1 {
		t.Errorf("retail series grew to %d rows", len(retail))
	}
}

// RecordBids applies the differs-from-last rule: a first bid is the
// baseline, a repeat inserts nothing, a change appends.
func TestRecordBidsSkipsRepeats(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	bid := func(price float64) []BidObservation {
		return []BidObservation{{ScryfallID: "ulamog-id", Finish: "normal", Source: "cardkingdom", Price: price}}
	}

	if n, err := s.RecordBids(bid(4.00)); err != nil || n != 1 {
		t.Fatalf("baseline: %d, %v; want 1 insert", n, err)
	}
	if n, err := s.RecordBids(bid(4.00)); err != nil || n != 0 {
		t.Fatalf("repeat: %d, %v; want nothing", n, err)
	}
	// Age the baseline so the change lands on a different timestamp — the
	// test runs inside one second, the real world does not.
	if _, err := s.db.Exec(
		`UPDATE card_bid_history SET as_of = '2026-07-01T00:00:00Z'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if n, err := s.RecordBids(bid(4.50)); err != nil || n != 1 {
		t.Fatalf("change: %d, %v; want 1 insert", n, err)
	}
	got, _ := s.BidSeries("ulamog-id", "nonfoil")
	if len(got) != 2 || got[0].Price != 4.00 || got[1].Price != 4.50 {
		t.Errorf("series = %+v, want baseline then the change, stored as nonfoil", got)
	}
}
