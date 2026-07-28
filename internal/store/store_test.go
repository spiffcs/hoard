package store

import (
	"path/filepath"
	"testing"
)

func f(v float64) *float64 { return &v }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleCard() Card {
	return Card{
		ScryfallID:      "abc-123",
		SetCode:         "uma",
		CollectorNumber: "7",
		Name:            "Ulamog, the Infinite Gyre",
		PriceUSD:        f(10.00),
		PriceUSDFoil:    f(25.00),
		ScryfallURL:     "https://scryfall.com/card/uma/7",
	}
}

func TestAddCardInsertAndIncrement(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddCard(sampleCard(), false, 2); err != nil {
		t.Fatalf("AddCard normal: %v", err)
	}
	if err := s.AddCard(sampleCard(), true, 1); err != nil {
		t.Fatalf("AddCard foil: %v", err)
	}
	// Adding the same non-foil again should increment, not duplicate.
	if err := s.AddCard(sampleCard(), false, 3); err != nil {
		t.Fatalf("AddCard normal again: %v", err)
	}

	cards, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.QtyNormal != 5 {
		t.Errorf("QtyNormal = %d, want 5", c.QtyNormal)
	}
	if c.QtyFoil != 1 {
		t.Errorf("QtyFoil = %d, want 1", c.QtyFoil)
	}
}

func TestUpdatePrices(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(sampleCard(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if err := s.UpdatePrices("abc-123", f(99.99), nil); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	c, err := s.Get("abc-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c == nil || c.PriceUSD == nil || *c.PriceUSD != 99.99 {
		t.Errorf("PriceUSD not updated: %+v", c)
	}
	if c.PriceUSDFoil != nil {
		t.Errorf("PriceUSDFoil should be nil, got %v", *c.PriceUSDFoil)
	}
}

func TestSetQuantitiesAndRemove(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(sampleCard(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	n, err := s.SetQuantities("abc-123", 4, 2)
	if err != nil {
		t.Fatalf("SetQuantities: %v", err)
	}
	if n != 1 {
		t.Fatalf("SetQuantities affected %d rows, want 1", n)
	}
	c, _ := s.Get("abc-123")
	if c.QtyNormal != 4 || c.QtyFoil != 2 {
		t.Errorf("quantities = %d/%d, want 4/2", c.QtyNormal, c.QtyFoil)
	}

	// Setting quantities on a missing card affects 0 rows.
	if n, _ := s.SetQuantities("missing", 1, 1); n != 0 {
		t.Errorf("SetQuantities on missing affected %d rows, want 0", n)
	}

	removed, err := s.Remove("abc-123")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Remove affected %d rows, want 1", removed)
	}
	if c, _ := s.Get("abc-123"); c != nil {
		t.Errorf("card still present after remove: %+v", c)
	}
}

func TestGetMissingReturnsNil(t *testing.T) {
	s := newTestStore(t)
	c, err := s.Get("nope")
	if err != nil {
		t.Fatalf("Get missing returned error: %v", err)
	}
	if c != nil {
		t.Errorf("want nil for missing card, got %+v", c)
	}
}
