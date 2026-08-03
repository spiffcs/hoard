package store

import (
	"fmt"
	"strings"
)

// User-named binders: the loose collection split into labelled parts. Every
// database starts with one (the default, shown as LooseName); the rest are
// ordinary kind='collection' rows a user creates, so nothing here needed a
// schema change and a single-binder database behaves exactly as before.

// binderSourceID is the stable source_id for a user-created binder, keeping
// the UNIQUE(source, source_id) constraint meaningful. It is derived from the
// name at creation and never changes afterwards, so renames are cheap.
func binderSourceID(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '-', r == '_':
			return '-'
		}
		return -1
	}, slug)
	return "binder:" + slug
}

// IsDefaultBinder reports whether a container is the built-in binder every
// database starts with — the one that cannot be removed, though it can be
// renamed like any other.
func IsDefaultBinder(c Container) bool { return c.SourceID == collectionSourceID }

// ListBinders returns every binder with rolled-up counts and value, the
// default binder first, then the rest by name — the order the browser's left
// pane shows them in.
func (s *Store) ListBinders() ([]DeckSummary, error) {
	// Creating on first use keeps a fresh database from listing zero binders.
	if _, err := s.collectionID(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.source, COALESCE(ct.source_url,''), COALESCE(ct.format,''),
       ct.source_id = '`+collectionSourceID+`' AS is_default,
       COUNT(e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM containers ct
LEFT JOIN card_entries e ON e.container_id = ct.id
LEFT JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinEntries+`
WHERE ct.kind = ?
GROUP BY ct.id
ORDER BY CASE WHEN ct.source_id = '`+collectionSourceID+`' THEN 0 ELSE 1 END, ct.name`,
		KindCollection)
	if err != nil {
		return nil, fmt.Errorf("listing binders: %w", err)
	}
	defer rows.Close()

	var out []DeckSummary
	for rows.Next() {
		var d DeckSummary
		d.Kind = KindCollection
		if err := rows.Scan(&d.ID, &d.Name, &d.Source, &d.SourceURL, &d.Format,
			&d.IsDefault, &d.DistinctCards, &d.TotalCopies, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// BinderByRef resolves a binder by numeric id, exact name, or a
// case-insensitive fragment of its name. The default binder also answers to
// the reserved aliases ("Binder", "Collection") whatever its current name.
func (s *Store) BinderByRef(ref string) (*Container, error) {
	if _, err := s.collectionID(); err != nil {
		return nil, err
	}
	return s.containerByRef(KindCollection, "binder", ref)
}

// CreateBinder makes a new empty binder and returns its id.
func (s *Store) CreateBinder(name string) (int64, error) {
	name, sid, err := s.validateNewBinderName(name)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(insertBinderSQL, KindCollection, name, sid, now(), now())
	if err != nil {
		return 0, fmt.Errorf("creating binder %q: %w", name, err)
	}
	return res.LastInsertId()
}

const insertBinderSQL = `
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, ?, 'manual', ?, ?, ?)`

// validateNewBinderName checks a proposed binder name against the creation
// rules without creating anything, so a batch import can vet every name before
// its transaction opens. Returns the trimmed name and its source_id slug.
func (s *Store) validateNewBinderName(name string) (trimmed, sourceID string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("a binder needs a name")
	}
	sid := binderSourceID(name)
	if sid == "binder:" {
		return "", "", fmt.Errorf("binder name %q has no usable characters", name)
	}
	// The reserved aliases always mean the default binder — even after it has
	// been renamed — so no other binder may claim them: an old export whose
	// Container column says "Binder" must keep landing in the default.
	if IsReservedBinderName(name) {
		return "", "", fmt.Errorf("%q is reserved for the default binder", name)
	}
	// Uniqueness is checked against display names, not just the slugged
	// source_id, so "Trade" cannot shadow an existing binder or collide with
	// a name that slugs identically.
	if existing, err := s.containerByRef(KindCollection, "binder", name); err == nil {
		return "", "", fmt.Errorf("a binder named %q already exists", existing.Name)
	}
	return name, sid, nil
}

// RenameBinder gives a binder a new display name. The default binder renames
// like any other — its identity is its source_id, not its name — but the
// reserved aliases stay its alone: only the default may take (or retake) the
// name "Binder", so old exports keep resolving to it.
func (s *Store) RenameBinder(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a binder needs a name")
	}
	c, err := s.containerByRef(KindCollection, "binder", fmt.Sprint(id))
	if err != nil {
		return err
	}
	if IsReservedBinderName(name) && !IsDefaultBinder(*c) {
		return fmt.Errorf("%q is reserved for the default binder", name)
	}
	if existing, err := s.containerByRef(KindCollection, "binder", name); err == nil && existing.ID != id {
		return fmt.Errorf("a binder named %q already exists", existing.Name)
	}
	_, err = s.db.Exec(`UPDATE containers SET name=?, updated_at=? WHERE id=?`, name, now(), id)
	return err
}

// DeleteBinder removes an empty binder. A binder still holding cards is
// refused rather than cascaded: unlike a deck, its entries represent loose
// cards that exist nowhere else in the database.
func (s *Store) DeleteBinder(id int64) error {
	c, err := s.containerByRef(KindCollection, "binder", fmt.Sprint(id))
	if err != nil {
		return err
	}
	if IsDefaultBinder(*c) {
		return fmt.Errorf("the default binder %q cannot be removed", c.Name)
	}
	var copies int
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(quantity),0) FROM card_entries WHERE container_id=?`,
		id).Scan(&copies); err != nil {
		return err
	}
	if copies > 0 {
		return fmt.Errorf("binder %q still holds %d cards; move or remove them first", c.Name, copies)
	}
	_, err = s.db.Exec(`DELETE FROM containers WHERE id=?`, id)
	return err
}
