package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func TestSchemaAcceptsOnlyTheEnumeratedFinishes(t *testing.T) {
	all := finish.All()
	if len(all) < 3 {
		t.Fatalf("finish.All() = %v, want at least nonfoil, foil and etched", all)
	}
	for _, table := range finishTables() {
		t.Run(table.name, func(t *testing.T) {
			for _, finish := range all {
				s := newTestStore(t)
				if err := table.insert(s, finish.String()); err != nil {
					t.Errorf("the schema rejects %q, which finish.All() lists: %v", finish, err)
				}
			}
			for _, text := range []string{"normal", "shiny", "", "Foil"} {
				s := newTestStore(t)
				if err := table.insert(s, text); err == nil {
					t.Errorf("the schema accepted finish %q, want a CHECK violation", text)
				}
			}
		})
	}
}

func TestSchemaRequiresAFinishOnEveryEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	var containerID int64
	if err := s.db.QueryRow(`SELECT container_id FROM card_entries LIMIT 1`).Scan(&containerID); err != nil {
		t.Fatalf("reading the container: %v", err)
	}
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, board, quantity)
VALUES (?, ?, 'main', 1)`, containerID, solRing().ID); err == nil {
		t.Error("an entry with no finish was accepted; the column still has a DEFAULT")
	}
}

type finishTable struct {
	name   string
	insert func(s *Store, finish string) error
}

func finishTables() []finishTable {
	return []finishTable{
		{"card_entries", func(s *Store, finish string) error {
			containerID, err := seedCard(s)
			if err != nil {
				return err
			}
			_, err = s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, 'nm', 'main', 1)`, containerID, ulamog().ID, finish)
			return err
		}},
		{"card_price_history", func(s *Store, finish string) error {
			if _, err := seedCard(s); err != nil {
				return err
			}
			_, err := s.db.Exec(`
INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
VALUES ('ulamog-id', ?, 1.00, 'mtgjson', '2026-01-01T00:00:00Z')`, finish)
			return err
		}},
		{"watches", func(s *Store, finish string) error {
			if _, err := seedCard(s); err != nil {
				return err
			}
			_, err := s.db.Exec(`
INSERT INTO watches (scryfall_id, display, finish, op, threshold, created_at)
VALUES ('ulamog-id', 'Ulamog', ?, 'above', 10.0, '2026-01-01T00:00:00Z')`, finish)
			return err
		}},
		{"card_bid_history", func(s *Store, finish string) error {
			if _, err := seedCard(s); err != nil {
				return err
			}
			_, err := s.db.Exec(`
INSERT INTO card_bid_history (scryfall_id, finish, price_usd, source, as_of)
VALUES ('ulamog-id', ?, 1.00, 'cardkingdom', '2026-01-01T00:00:00Z')`, finish)
			return err
		}},
	}
}

func seedCard(s *Store) (int64, error) {
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		return 0, err
	}
	var containerID int64
	err := s.db.QueryRow(`SELECT container_id FROM card_entries LIMIT 1`).Scan(&containerID)
	return containerID, err
}
