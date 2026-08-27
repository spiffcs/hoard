package store

import (
	"fmt"
	"slices"

	"github.com/spiffcs/hoard/internal/finish"
)

type FinishFix struct {
	Name            string
	SetCode         string
	CollectorNumber string
	Container       string
	Board           string
	From            finish.Finish

	To finish.Finish

	Available []finish.Finish
	Quantity  int
}

func FinishIsAvailable(fin finish.Finish, available []finish.Finish) bool {
	if len(available) == 0 {
		return true
	}
	return slices.Contains(available, fin)
}

func CorrectFinish(fin finish.Finish, available []finish.Finish) (finish.Finish, bool) {
	if FinishIsAvailable(fin, available) || len(available) != 1 {
		return fin, false
	}
	return available[0], true
}

func (s *Store) RepairFinishes(available map[string][]finish.Finish) (fixed, ambiguous []FinishFix, err error) {

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
SELECT e.id, e.container_id, ct.name, e.scryfall_id, e.finish, e.condition,
       e.board, e.quantity,
       c.name, c.set_code, c.collector_number
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
ORDER BY c.name`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading entries: %w", err)
	}
	defer rows.Close()

	type target struct {
		id          int64
		containerID int64
		scryfallID  string
		condition   string
		board       string
		from, to    finish.Finish
		quantity    int
	}
	var todo []target
	for rows.Next() {
		var t target
		var f FinishFix
		if err := rows.Scan(&t.id, &t.containerID, &f.Container, &t.scryfallID, &t.from,
			&t.condition, &t.board, &t.quantity,
			&f.Name, &f.SetCode, &f.CollectorNumber); err != nil {
			return nil, nil, err
		}
		finishes, known := available[t.scryfallID]
		if !known || len(finishes) == 0 {
			continue
		}
		if FinishIsAvailable(t.from, finishes) {
			continue
		}

		f.Board, f.From, f.Quantity = t.board, t.from, t.quantity
		to, ok := CorrectFinish(t.from, finishes)
		if !ok {
			f.Available = finishes
			ambiguous = append(ambiguous, f)
			continue
		}
		t.to, f.To = to, to
		fixed = append(fixed, f)
		todo = append(todo, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows.Close()
	if len(todo) == 0 {
		return nil, ambiguous, nil
	}

	for _, t := range todo {

		if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board, COALESCE(purchase_price, -1))
DO UPDATE SET quantity = quantity + excluded.quantity`,
			t.containerID, t.scryfallID, t.to, t.condition, t.board, t.quantity); err != nil {
			return nil, nil, fmt.Errorf("moving entry to %s: %w", t.to, err)
		}

		if _, err := tx.Exec(`DELETE FROM card_entries WHERE id = ?`, t.id); err != nil {
			return nil, nil, fmt.Errorf("removing old %s entry: %w", t.from, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return fixed, ambiguous, nil
}
