package store

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type CompendiumPrinting struct {
	Card     scryfall.Card
	Finishes []finish.Finish
}

func (s *Store) CollectionID() (int64, error) { return s.collectionID() }

func (s *Store) SeedCompendiumPrintings(containerID int64, ps []CompendiumPrinting) (printings, entries int, err error) {
	if len(ps) == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	cards := make([]scryfall.Card, len(ps))
	for i, p := range ps {
		cards[i] = p.Card
	}
	if err := upsertPrintingsTx(tx, cards); err != nil {
		return 0, 0, err
	}

	stmt, err := tx.Prepare(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, 'unknown', 'main', 1)
ON CONFLICT(container_id, scryfall_id, finish, condition, board) DO NOTHING`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	for _, p := range ps {
		for _, fin := range p.Finishes {
			if err := validFinish(fin); err != nil {
				return 0, 0, fmt.Errorf("seeding %s: %w", p.Card.Name, err)
			}
			res, err := stmt.Exec(containerID, p.Card.ID, fin)
			if err != nil {
				return 0, 0, fmt.Errorf("seeding %s: %w", p.Card.Name, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return 0, 0, err
			}
			entries += int(n)
		}
	}
	return len(ps), entries, tx.Commit()
}
