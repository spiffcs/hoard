package store

import (
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

func TestBidSeriesReturnsTheWholeSeries(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
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

	got, err := s.BidSeries("ulamog-id", finish.Nonfoil)
	if err != nil {
		t.Fatalf("BidSeries: %v", err)
	}
	if len(got) != 2 || got[0].Price != 4.0 || got[1].Price != 5.0 {
		t.Errorf("series = %+v, want the two bids oldest first", got)
	}

	for _, p := range got {
		if p.Price == 10.0 {
			t.Errorf("retail observation leaked into the bid series: %+v", p)
		}
	}
}

func TestBackfillBidsBoundsAgainstOwnFirstObservations(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

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
			obs("2026-07-08", finish.Nonfoil, 4.00),
			obs("2026-07-09", finish.Nonfoil, 5.00),
			obs("2026-07-10", finish.Nonfoil, 5.90),
		},
	})
	if err != nil {
		t.Fatalf("BackfillBids: %v", err)
	}
	if inserted != 2 || cards != 1 {
		t.Errorf("inserted %d across %d cards, want the two pre-era days", inserted, cards)
	}
	got, _ := s.BidSeries("ulamog-id", finish.Nonfoil)
	want := []float64{4.00, 5.00, 6.00}
	var prices []float64
	for _, p := range got {
		prices = append(prices, p.Price)
	}
	if !slices.Equal(prices, want) {
		t.Errorf("series = %v, want %v (translated to nonfoil, live row last)", prices, want)
	}

	retail, _ := s.PriceSeries("ulamog-id", finish.Nonfoil)
	if len(retail) != 1 {
		t.Errorf("retail series grew to %d rows", len(retail))
	}
}

func TestRecordBidsSkipsRepeats(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	bid := func(price float64) []BidObservation {
		return []BidObservation{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Source: "cardkingdom", Price: price}}
	}

	if n, err := s.RecordBids(bid(4.00)); err != nil || n != 1 {
		t.Fatalf("baseline: %d, %v; want 1 insert", n, err)
	}
	if n, err := s.RecordBids(bid(4.00)); err != nil || n != 0 {
		t.Fatalf("repeat: %d, %v; want nothing", n, err)
	}

	if _, err := s.db.Exec(
		`UPDATE card_bid_history SET as_of = '2026-07-01T00:00:00Z'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if n, err := s.RecordBids(bid(4.50)); err != nil || n != 1 {
		t.Fatalf("change: %d, %v; want 1 insert", n, err)
	}
	got, _ := s.BidSeries("ulamog-id", finish.Nonfoil)
	if len(got) != 2 || got[0].Price != 4.00 || got[1].Price != 4.50 {
		t.Errorf("series = %+v, want baseline then the change, stored as nonfoil", got)
	}
}
