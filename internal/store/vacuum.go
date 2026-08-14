package store

import "fmt"

func (s *Store) VacuumPrintings() (removed int, err error) {
	res, err := s.db.Exec(`
DELETE FROM cards WHERE scryfall_id IN (
  SELECT c.scryfall_id FROM cards c
  WHERE NOT EXISTS (SELECT 1 FROM card_entries e WHERE e.scryfall_id = c.scryfall_id)
    AND NOT EXISTS (SELECT 1 FROM watches w WHERE w.scryfall_id = c.scryfall_id))`)
	if err != nil {
		return 0, fmt.Errorf("deleting orphaned printings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := s.Compact(); err != nil {
			return int(n), err
		}
	}
	return int(n), nil
}

func (s *Store) Compact() error {
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("compacting the database: %w", err)
	}
	return nil
}
