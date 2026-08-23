package store

import "fmt"

func (s *Store) SetBinderCounted(id int64, counted bool) error {
	v := 0
	if counted {
		v = 1
	}
	res, err := s.db.Exec(
		`UPDATE containers SET counted = ?, updated_at = ? WHERE id = ? AND kind = ?`,
		v, now(), id, KindCollection)
	if err != nil {
		return fmt.Errorf("setting whether binder %d counts: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no binder with id %d", id)
	}
	return nil
}

const countedEntries = `
    JOIN containers ctc ON ctc.id = e.container_id AND ctc.counted = 1`

const countedQuantity = `
    CASE WHEN ctc.counted = 1 THEN e.quantity ELSE 0 END`
