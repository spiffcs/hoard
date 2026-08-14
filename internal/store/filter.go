package store

import (
	"fmt"
	"strings"
)

type NumCond struct {
	Op    string
	Value float64
}

type TraitFilter struct {
	Rarities []string
	Types    []string
	Artists  []string
	Layouts  []string
	SetNames []string
	Colors   []string
	CMC      []NumCond
}

func (f TraitFilter) Empty() bool {
	return len(f.Rarities) == 0 && len(f.Types) == 0 && len(f.Artists) == 0 &&
		len(f.Layouts) == 0 && len(f.SetNames) == 0 && len(f.Colors) == 0 &&
		len(f.CMC) == 0
}

var validOps = map[string]bool{">": true, ">=": true, "<": true, "<=": true, "=": true}

func (s *Store) MatchingCardIDs(f TraitFilter) (map[string]bool, error) {
	if f.Empty() {
		return nil, nil
	}

	var where []string
	var args []any

	like := func(col string, values []string) {
		for _, v := range values {
			where = append(where, col+" IS NOT NULL AND lower("+col+") LIKE ?")
			args = append(args, "%"+strings.ToLower(v)+"%")
		}
	}
	like("type_line", f.Types)
	like("artist", f.Artists)
	like("layout", f.Layouts)
	like("set_name", f.SetNames)

	for _, v := range f.Rarities {
		where = append(where, "rarity IS NOT NULL AND lower(rarity) = ?")
		args = append(args, strings.ToLower(v))
	}

	for _, c := range f.Colors {

		where = append(where,
			`EXISTS (SELECT 1 FROM json_each(cards.color_identity) WHERE value = ?)`)
		args = append(args, strings.ToUpper(c))
	}

	for _, c := range f.CMC {
		if !validOps[c.Op] {
			return nil, fmt.Errorf("invalid comparison %q", c.Op)
		}
		where = append(where, "cmc IS NOT NULL AND cmc "+c.Op+" ?")
		args = append(args, c.Value)
	}

	rows, err := s.db.Query(
		`SELECT scryfall_id FROM cards INDEXED BY cards_trait_filter WHERE `+
			strings.Join(where, " AND "), args...)
	if err != nil {
		return nil, fmt.Errorf("filtering cards: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) EnrichedCount() (enriched, total int, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(SUM(`+enrichedExpr+`), 0), COUNT(*) FROM cards c`).
		Scan(&enriched, &total)
	return enriched, total, err
}
