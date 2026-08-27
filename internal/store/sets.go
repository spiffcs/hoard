package store

import (
	"fmt"
	"slices"
	"strings"
)

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
JOIN cards c ON c.scryfall_id = e.scryfall_id` + countedEntries + `
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
       e.finish, e.condition, NULL AS purchase_price,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id`+countedEntries+`
`+altJoinCards+`
WHERE c.set_code = ?
GROUP BY c.scryfall_id, e.finish, e.condition
ORDER BY value DESC, c.name`, setCode)
	if err != nil {
		return nil, fmt.Errorf("listing set %s: %w", setCode, err)
	}
	return scanCollectionRows(rows)
}

type UnownedRow struct {
	CollectionRow

	Where string
}

func (s *Store) SetUnowned(setCode string) ([]UnownedRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceExpr)+`,
       COALESCE(SUM(`+shelvedQuantity+`), 0) AS quantity,
       COALESCE(SUM(`+shelvedQuantity+` * `+entryValue+`), 0) AS value,
       COALESCE(GROUP_CONCAT(DISTINCT CASE
           WHEN `+shelved+` THEN ct.name END), '') AS shelves
FROM cards c
LEFT JOIN card_entries e ON e.scryfall_id = c.scryfall_id
LEFT JOIN containers ct ON ct.id = e.container_id
`+altJoinCards+`
WHERE c.set_code = ?
GROUP BY c.scryfall_id
HAVING COALESCE(SUM(CASE
           WHEN ct.counted = 1 THEN e.quantity ELSE 0 END), 0) = 0
ORDER BY value DESC, c.name`, setCode)
	if err != nil {
		return nil, fmt.Errorf("listing what set %s is missing: %w", setCode, err)
	}
	defer rows.Close()

	var out []UnownedRow
	for rows.Next() {
		var r UnownedRow
		var aux cardAux
		var shelves string
		if err := rows.Scan(append(cardScanDest(&r.Card, &aux),
			&r.Quantity, &r.Value, &shelves)...); err != nil {
			return nil, err
		}
		aux.apply(&r.Card)
		r.Where = tidyShelves(shelves)
		out = append(out, r)
	}
	return out, rows.Err()
}

const shelved = `ct.counted = 0 AND e.quantity > 0`

const shelvedQuantity = `CASE WHEN ` + shelved + ` THEN e.quantity ELSE 0 END`

func tidyShelves(s string) string {
	if s == "" {
		return ""
	}
	names := strings.Split(s, ",")
	slices.Sort(names)
	return strings.Join(names, ", ")
}
