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

func (s *Store) MisfinishedEntries(available map[string][]finish.Finish) (
	fixable, ambiguous []FinishFix, err error) {

	rows, err := s.db.Query(`
SELECT ct.name, e.scryfall_id, e.finish, e.board, e.quantity,
       c.name, c.set_code, c.collector_number
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
ORDER BY c.name`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading entries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var f FinishFix
		var scryfallID string
		if err := rows.Scan(&f.Container, &scryfallID, &f.From, &f.Board, &f.Quantity,
			&f.Name, &f.SetCode, &f.CollectorNumber); err != nil {
			return nil, nil, err
		}
		finishes, known := available[scryfallID]
		if !known || len(finishes) == 0 {
			continue
		}
		if FinishIsAvailable(f.From, finishes) {
			continue
		}
		to, ok := CorrectFinish(f.From, finishes)
		if !ok {
			f.Available = finishes
			ambiguous = append(ambiguous, f)
			continue
		}
		f.To = to
		fixable = append(fixable, f)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return fixable, ambiguous, nil
}
