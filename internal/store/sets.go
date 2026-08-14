package store

import "fmt"

type SetSummary struct {
	Code       string
	Name       string
	ReleasedAt string
	Copies     int
	Value      float64
}

func (s *Store) SetsHeld() ([]SetSummary, error) {
	rows, err := s.db.Query(`
SELECT c.set_code,
       COALESCE(MAX(c.set_name), UPPER(c.set_code)) AS set_name,
       COALESCE(MAX(c.released_at), '') AS released_at,
       COALESCE(SUM(e.quantity), 0) AS copies,
       COALESCE(SUM(e.quantity * ` + entryValue + `), 0) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
` + altJoinCards + `
GROUP BY c.set_code
ORDER BY MAX(c.released_at) DESC, c.set_code`)
	if err != nil {
		return nil, fmt.Errorf("listing sets held: %w", err)
	}
	defer rows.Close()
	var out []SetSummary
	for rows.Next() {
		var r SetSummary
		if err := rows.Scan(&r.Code, &r.Name, &r.ReleasedAt, &r.Copies, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetByFinish(setCode string) ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish, e.condition,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
WHERE c.set_code = ?
GROUP BY c.scryfall_id, e.finish, e.condition
ORDER BY value DESC, c.name`, setCode)
	if err != nil {
		return nil, fmt.Errorf("listing set %s: %w", setCode, err)
	}
	return scanCollectionRows(rows)
}
