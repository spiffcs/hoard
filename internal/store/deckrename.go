package store

import (
	"fmt"
	"strings"
)

func (s *Store) RenameDeck(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a deck needs a name")
	}
	res, err := s.db.Exec(`
UPDATE containers SET name=?, name_locked=1, updated_at=? WHERE id=? AND kind=?`,
		name, now(), id, KindDeck)
	if err != nil {
		return fmt.Errorf("renaming deck: %w", err)
	}
	return oneRow(res, "no deck #%d", id)
}

func (s *Store) UnlockDeckName(id int64) error {
	res, err := s.db.Exec(`
UPDATE containers SET name_locked=0, updated_at=? WHERE id=? AND kind=?`,
		now(), id, KindDeck)
	if err != nil {
		return fmt.Errorf("releasing deck name: %w", err)
	}
	return oneRow(res, "no deck #%d", id)
}
