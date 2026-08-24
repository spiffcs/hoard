package store

import "fmt"

func (s *Store) SetContainerCounted(id int64, counted bool) error {
	v := 0
	if counted {
		v = 1
	}
	res, err := s.db.Exec(
		`UPDATE containers SET counted = ?, updated_at = ?
WHERE id = ? AND kind IN (?, ?)`,
		v, now(), id, KindCollection, KindDeck)
	if err != nil {
		return fmt.Errorf("setting whether container %d counts: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no binder or deck with id %d", id)
	}
	return nil
}

const countedEntries = `
    JOIN containers ctc ON ctc.id = e.container_id AND ctc.counted = 1`

const countedQuantity = `
    CASE WHEN ctc.counted = 1 THEN e.quantity ELSE 0 END`
