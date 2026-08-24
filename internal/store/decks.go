package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (s *Store) DeckBySource(source, sourceID string) (id int64, name string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT id, name FROM containers WHERE source=? AND source_id=?`,
		source, sourceID).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return id, name, true, nil
}

func (s *Store) UpsertDeck(meta DeckMeta, entries []Entry) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := upsertDeckTx(tx, meta, entries)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func upsertDeckTx(tx *sql.Tx, meta DeckMeta, entries []Entry) (int64, error) {
	ts := now()
	if _, err := tx.Exec(`
INSERT INTO containers (kind, name, source, source_id, source_url, format, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source, source_id) DO UPDATE SET
    name       = CASE WHEN containers.name_locked = 1
                      THEN containers.name ELSE excluded.name END,
    source_url = excluded.source_url,
    format     = excluded.format,
    updated_at = excluded.updated_at`,
		KindDeck, meta.Name, meta.Source, meta.SourceID, meta.SourceURL, meta.Format, ts, ts); err != nil {
		return 0, fmt.Errorf("upserting deck %q: %w", meta.Name, err)
	}

	var id int64
	if err := tx.QueryRow(`SELECT id FROM containers WHERE source=? AND source_id=?`,
		meta.Source, meta.SourceID).Scan(&id); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`DELETE FROM card_entries WHERE container_id=?`, id); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(entryAccumulateSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(id, e.ScryfallID, e.Finish, orUnknown(e.Condition),
			e.Board, e.Quantity); err != nil {
			return 0, fmt.Errorf("inserting deck entry: %w", err)
		}
	}
	return id, nil
}

const containerSelect = `
SELECT ct.id, ct.kind, ct.name, ct.source,
       COALESCE(ct.source_id,''), COALESCE(ct.source_url,''), COALESCE(ct.format,''),
       COALESCE(ct.parent_id,0)
FROM containers ct WHERE ct.kind=?`

func (s *Store) ListDecks() ([]DeckSummary, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.source, COALESCE(ct.source_url,''), COALESCE(ct.format,''),
       COALESCE(ct.parent_id,0) AS parent_id,
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
ORDER BY ct.name`, KindDeck)
	if err != nil {
		return nil, fmt.Errorf("listing decks: %w", err)
	}
	defer rows.Close()

	var out []DeckSummary
	for rows.Next() {
		var d DeckSummary
		d.Kind = KindDeck
		if err := rows.Scan(&d.ID, &d.Name, &d.Source, &d.SourceURL, &d.Format, &d.ParentID,
			&d.Counted, &d.DistinctCards, &d.TotalCopies, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanContainer(sc interface{ Scan(...any) error }) (*Container, error) {
	var c Container
	if err := sc.Scan(&c.ID, &c.Kind, &c.Name, &c.Source, &c.SourceID, &c.SourceURL, &c.Format,
		&c.ParentID); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) DeckByRef(ref string) (*Container, error) {
	return s.containerByRef(KindDeck, "deck", ref)
}

func (s *Store) containerByRef(kind, noun, ref string) (*Container, error) {

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		c, err := scanContainer(s.db.QueryRow(containerSelect+` AND ct.id=?`, kind, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("no %s matching %q", noun, ref)
		}
		return c, err
	}

	c, err := scanContainer(s.db.QueryRow(
		containerSelect+` AND ct.name=? COLLATE NOCASE`, kind, ref))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if kind == KindCollection && IsReservedBinderName(ref) {
		id, err := s.collectionID()
		if err != nil {
			return nil, err
		}
		return scanContainer(s.db.QueryRow(containerSelect+` AND ct.id=?`, kind, id))
	}

	rows, err := s.db.Query(
		containerSelect+` AND ct.name LIKE ? ESCAPE '\' ORDER BY ct.name`,
		kind, "%"+escapeLike(ref)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*Container
	for rows.Next() {
		m, err := scanContainer(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no %s matching %q", noun, ref)
	case 1:
		return matches[0], nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d %ss:", ref, len(matches), noun)
		for _, m := range matches[:min(len(matches), 5)] {
			fmt.Fprintf(&b, "\n  %s", m.Name)
		}
		if len(matches) > 5 {
			fmt.Fprintf(&b, "\n  … and %d more", len(matches)-5)
		}
		b.WriteString("\nUse a longer fragment or the full name.")
		return nil, errors.New(b.String())
	}
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *Store) DeckEntries(containerID int64) ([]EntryView, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish, e.condition, e.board, e.quantity
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
WHERE e.container_id = ?
ORDER BY
    CASE e.board WHEN 'commander' THEN 0 WHEN 'main' THEN 1 WHEN 'side' THEN 2 ELSE 3 END,
    c.name`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntryView
	for rows.Next() {
		var v EntryView
		var aux cardAux
		if err := rows.Scan(append(cardScanDest(&v.Card, &aux),
			&v.Finish, &v.Condition, &v.Board, &v.Quantity)...); err != nil {
			return nil, err
		}
		aux.apply(&v.Card)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RemoveContainer(id int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM containers WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
