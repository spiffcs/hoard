package store

import (
	"fmt"
	"strings"
)

func containerSlug(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '-', r == '_':
			return '-'
		}
		return -1
	}, strings.ToLower(strings.TrimSpace(name)))
}

func binderSourceID(name string) string { return "binder:" + containerSlug(name) }

func IsDefaultBinder(c Container) bool { return c.SourceID == collectionSourceID }

func (s *Store) ListBinders() ([]DeckSummary, error) {

	if _, err := s.collectionID(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.source, COALESCE(ct.source_url,''), COALESCE(ct.format,''),
       ct.source_id = '`+collectionSourceID+`' AS is_default,
       ct.counted,
       -- COUNT(DISTINCT ...) rather than COUNT(...): the column means distinct
       -- printings, which is what CollectionTotals has always reported and what
       -- the JSON model documents. Counting rows instead made a card held in two
       -- finishes count twice, so the same binder read 194 here and 190 in the
       -- summary. Condition would have widened that gap again, since a card
       -- held NM and LP is two rows and one printing.
       COUNT(DISTINCT e.scryfall_id) AS distinct_cards,
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
			&d.IsDefault, &d.Counted, &d.DistinctCards, &d.TotalCopies, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) BinderByRef(ref string) (*Container, error) {
	if _, err := s.collectionID(); err != nil {
		return nil, err
	}
	return s.containerByRef(KindCollection, "binder", ref)
}

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

func (s *Store) validateNewBinderName(name string) (trimmed, sourceID string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("a binder needs a name")
	}
	sid := binderSourceID(name)
	if sid == "binder:" {
		return "", "", fmt.Errorf("binder name %q has no usable characters", name)
	}

	if IsReservedBinderName(name) {
		return "", "", fmt.Errorf("%q is reserved for the default binder", name)
	}

	if existing, err := s.containerByRef(KindCollection, "binder", name); err == nil {
		return "", "", fmt.Errorf("a binder named %q already exists", existing.Name)
	}
	return name, sid, nil
}

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
