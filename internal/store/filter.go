package store

import (
	"fmt"
	"strings"
)

// NumCond is one numeric comparison, e.g. `> 3`.
type NumCond struct {
	Op    string // > >= < <= =
	Value float64
}

// TraitFilter selects printings by the descriptive columns migration v5 derives
// from the stored Scryfall document.
//
// It covers only the traits, not the holding: how many copies are held and in
// which finish is a property of an entry, not of a printing, and belongs to
// whoever is looking at the rows. Keeping the split here means one query answers
// "which printings are red mythics" for the loose collection and every deck
// alike.
//
// Within a field the values are ANDed, not ORed — `type:legendary type:creature`
// asks for cards that are both, which is what typing two words means.
type TraitFilter struct {
	Rarities []string // substring, case-insensitive
	Types    []string
	Artists  []string
	Layouts  []string
	SetNames []string
	Colors   []string // single letters from color_identity, all must be present
	CMC      []NumCond
}

// Empty reports whether the filter would match everything, so a caller can skip
// the query entirely.
func (f TraitFilter) Empty() bool {
	return len(f.Rarities) == 0 && len(f.Types) == 0 && len(f.Artists) == 0 &&
		len(f.Layouts) == 0 && len(f.SetNames) == 0 && len(f.Colors) == 0 &&
		len(f.CMC) == 0
}

// validOps guards the only place a filter contributes SQL text rather than a
// bound parameter. Comparison operators cannot be parameterised, so they are
// matched against this set instead of being interpolated on trust.
var validOps = map[string]bool{">": true, ">=": true, "<": true, "<=": true, "=": true}

// MatchingCardIDs returns the Scryfall IDs of every catalogued printing the
// filter accepts.
//
// A set of ids rather than rows: the caller already holds the rows it is
// showing, from the collection listing or a deck's entries, and intersecting
// with a set leaves that path untouched. It also means one query serves both
// panes instead of a filtered variant of each listing.
//
// Every value is bound. The only SQL assembled from input is the comparison
// operator, checked against validOps first.
func (s *Store) MatchingCardIDs(f TraitFilter) (map[string]bool, error) {
	if f.Empty() {
		return nil, nil
	}

	var where []string
	var args []any

	// Trait columns are NULL until a refresh stores a document to derive them
	// from, and NULL fails LIKE silently. Asking for a trait therefore also
	// asserts the card has one, so an un-refreshed hoard returns nothing rather
	// than an arbitrary subset — visibly empty is recoverable, quietly partial
	// is not.
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

	// Rarity is matched exactly, unlike the free-text columns above. It is a
	// closed set of six values, two of which contain each other: a substring
	// match makes rarity:common return every uncommon, which is both wrong and
	// the exact opposite of what was asked for.
	for _, v := range f.Rarities {
		where = append(where, "rarity IS NOT NULL AND lower(rarity) = ?")
		args = append(args, strings.ToLower(v))
	}

	for _, c := range f.Colors {
		// color_identity is the JSON array Scryfall sends, so membership is a
		// json_each lookup. A LIKE would make color:U match a UB card's "B"
		// entry as readily as its "U" one.
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

	// INDEXED BY, not planner's choice: every predicate column here is a
	// VIRTUAL column over raw_json, so a table scan re-parses ~5.4 KB of JSON
	// per row per keystroke — the browse filter's measured stutter. The v27
	// index stores those computed values plus scryfall_id, but SQLite's cost
	// model sees no range constraint to seek on (the LIKEs are substring
	// matches) and picks the table scan anyway; naming the index makes the
	// predicates read from index leaves instead (measured 9x on a 5k-card
	// catalog). If the index is ever dropped this query fails loudly rather
	// than quietly reverting to the parse-everything plan.
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

// EnrichedCount reports how many catalogued printings have a stored Scryfall
// document, so a caller can tell "no card matched" from "no card has traits to
// match against yet" and point at update-prices instead of at the filter.
//
// Counted through enrichedExpr rather than the COUNT(raw_json) this used to
// open-code. Both spellings happen to agree, which is precisely the problem: a
// third wording of one rule is a third thing to keep in step, and it read as an
// independent decision about what "enriched" means.
//
// SUM is COALESCEd because it returns NULL over zero rows where COUNT returned
// 0 — an empty catalog is the state a fresh hoard is in, so that difference
// would have surfaced on somebody's first run.
func (s *Store) EnrichedCount() (enriched, total int, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(SUM(`+enrichedExpr+`), 0), COUNT(*) FROM cards c`).
		Scan(&enriched, &total)
	return enriched, total, err
}
