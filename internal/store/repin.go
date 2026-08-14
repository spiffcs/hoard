package store

import "fmt"

func (s *Store) RepointDeckPrintings(containerID int64, mapping map[string]string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	moved := 0
	for from, to := range mapping {
		rows, err := tx.Query(`
SELECT finish, condition, board, quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ?`, containerID, from)
		if err != nil {
			return 0, fmt.Errorf("reading entries of %s: %w", from, err)
		}
		type entryRow struct {
			finish, condition, board string
			qty                      int
		}
		var moves []entryRow
		for rows.Next() {
			var r entryRow
			if err := rows.Scan(&r.finish, &r.condition, &r.board, &r.qty); err != nil {
				rows.Close()
				return 0, err
			}
			moves = append(moves, r)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		for _, r := range moves {
			if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
				containerID, to, r.finish, r.condition, r.board, r.qty); err != nil {
				return 0, fmt.Errorf("re-pointing %s to %s: %w", from, to, err)
			}
			moved++
		}
		if _, err := tx.Exec(`
DELETE FROM card_entries WHERE container_id = ? AND scryfall_id = ?`,
			containerID, from); err != nil {
			return 0, err
		}
	}
	return moved, tx.Commit()
}
