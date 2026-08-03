package store

// The by-set reads behind the browser's sets mode: the left pane's per-set
// rollup and one set's holdings. A set is a lens over the hoard, not a
// container — nothing here touches card_entries beyond reading it.

import "fmt"

// SetSummary is one Magic set the hoard holds cards from, with rolled-up
// copies and value. Name falls back to the upper-cased code and ReleasedAt
// to "" until a price run stores the printings' Scryfall documents —
// set_name and released_at are generated columns over raw_json and read
// NULL before then.
type SetSummary struct {
	Code       string
	Name       string
	ReleasedAt string
	Copies     int
	Value      float64
}

// SetsHeld returns every set with at least one held card, newest release
// first; sets whose release date is unknown sort last (SQLite treats NULL
// as smallest, so DESC pushes it to the bottom), then by code for a stable
// order. MAX skips NULLs, so one enriched printing names the whole set.
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

// SetByFinish returns one set's holdings across every container, one row
// per printing and finish — AllByFinish narrowed to a set. Distinct
// printings stay distinct: a set listing is about which printings you
// hold, so nothing merges by name.
func (s *Store) SetByFinish(setCode string) ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
WHERE c.set_code = ?
GROUP BY c.scryfall_id, e.finish
ORDER BY value DESC, c.name`, setCode)
	if err != nil {
		return nil, fmt.Errorf("listing set %s: %w", setCode, err)
	}
	return scanCollectionRows(rows)
}
