package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestVacuumPrintings(t *testing.T) {
	s := newTestStore(t)
	held := scryfall.Card{ID: "held-1", Set: "uma", CollectorNumber: "1",
		Name: "Held", ScryfallURL: "http://x", PriceUSD: f(2)}
	orphan := scryfall.Card{ID: "orphan-1", Set: "hob", CollectorNumber: "2",
		Name: "Orphan", ScryfallURL: "http://x", PriceUSD: f(3)}
	watched := scryfall.Card{ID: "watched-1", Set: "mh3", CollectorNumber: "3",
		Name: "Watched", ScryfallURL: "http://x", PriceUSD: f(4)}
	if err := s.UpsertPrintings([]scryfall.Card{held, orphan, watched}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := s.AddCardFinish(held, finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("watched-1", "Watched", finish.Nonfoil, "under", 5); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	if _, err := s.db.Exec(`
INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
VALUES ('orphan-1', 'nonfoil', 3, 'test', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	removed, err := s.VacuumPrintings()
	if err != nil {
		t.Fatalf("VacuumPrintings: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want the orphan alone", removed)
	}
	var cards, history int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cards`).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_price_history
WHERE scryfall_id = 'orphan-1'`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if cards != 2 || history != 0 {
		t.Errorf("cards = %d, orphan history = %d; want 2 survivors and no history", cards, history)
	}

	if removed, err = s.VacuumPrintings(); err != nil || removed != 0 {
		t.Errorf("second vacuum = %d, %v; want a clean no-op", removed, err)
	}
}
