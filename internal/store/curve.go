package store

import (
	"fmt"
	"slices"
)

type CurveRow struct {
	Mana   int
	Copies int
}

type Curve struct {
	Rows    []CurveRow
	Lands   int
	Unknown int
}

func (c Curve) Spells() int {
	total := 0
	for _, r := range c.Rows {
		total += r.Copies
	}
	return total
}

const (
	curveLand    = "land"
	curveUnknown = "unknown"
	curveSpell   = "spell"
)

const curveKind = `
CASE WHEN c.type_line LIKE '%Land%' THEN '` + curveLand + `'
     WHEN c.cmc IS NULL THEN '` + curveUnknown + `'
     ELSE '` + curveSpell + `' END`

const curveMana = `
CASE WHEN c.type_line LIKE '%Land%' OR c.cmc IS NULL THEN 0
     ELSE CAST(c.cmc AS INTEGER) END`

func (s *Store) DeckCurve(containerID int64, board string) (Curve, error) {
	rows, err := s.db.Query(`
SELECT `+curveKind+` AS kind, `+curveMana+` AS mana, SUM(e.quantity) AS copies
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE e.container_id = ? AND e.board = ? AND e.quantity > 0
GROUP BY kind, mana
ORDER BY mana`, containerID, board)
	if err != nil {
		return Curve{}, fmt.Errorf("reading the curve of deck %d: %w", containerID, err)
	}
	defer rows.Close()

	var out Curve
	for rows.Next() {
		var kind string
		var mana, copies int
		if err := rows.Scan(&kind, &mana, &copies); err != nil {
			return Curve{}, err
		}
		switch kind {
		case curveLand:
			out.Lands += copies
		case curveUnknown:
			out.Unknown += copies
		default:
			out.Rows = append(out.Rows, CurveRow{Mana: mana, Copies: copies})
		}
	}
	if err := rows.Err(); err != nil {
		return Curve{}, err
	}
	slices.SortFunc(out.Rows, func(a, b CurveRow) int { return a.Mana - b.Mana })
	return out, nil
}
