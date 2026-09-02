package store

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

type HeldPlace struct {
	Name          string
	Finish        finish.Finish
	ContainerName string
}

func (s *Store) WhereHeld() ([]HeldPlace, error) {
	rows, err := s.db.Query(`
SELECT c.name, e.finish, ct.name
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id AND ct.counted = 1
GROUP BY c.name, e.finish, ct.id
ORDER BY c.name, e.finish,
         CASE ct.kind WHEN '` + KindCollection + `' THEN 0 ELSE 1 END, ct.name`)
	if err != nil {
		return nil, fmt.Errorf("reading where cards are held: %w", err)
	}
	defer rows.Close()

	var out []HeldPlace
	for rows.Next() {
		var p HeldPlace
		if err := rows.Scan(&p.Name, &p.Finish, &p.ContainerName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
