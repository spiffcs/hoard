package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func folderSourceID(name string) string { return "folder:" + containerSlug(name) }

func validateFolderName(name string) (trimmed, sourceID string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("a folder needs a name")
	}
	sid := folderSourceID(name)
	if sid == "folder:" {
		return "", "", fmt.Errorf("folder name %q has no usable characters", name)
	}
	return name, sid, nil
}

func (s *Store) folderByName(name string) (*Container, error) {
	return scanContainer(s.db.QueryRow(
		containerSelect+` AND ct.name=? COLLATE NOCASE`, KindFolder, name))
}

func (s *Store) CreateFolder(name string) (int64, error) {
	name, sid, err := validateFolderName(name)
	if err != nil {
		return 0, err
	}
	if existing, err := s.folderByName(name); err == nil {
		return 0, fmt.Errorf("a folder named %q already exists", existing.Name)
	}
	res, err := s.db.Exec(`
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, ?, 'manual', ?, ?, ?)`, KindFolder, name, sid, now(), now())
	if err != nil {
		return 0, fmt.Errorf("creating folder %q: %w", name, err)
	}
	return res.LastInsertId()
}

func (s *Store) RenameFolder(id int64, name string) error {
	name, sid, err := validateFolderName(name)
	if err != nil {
		return err
	}
	if existing, err := s.folderByName(name); err == nil && existing.ID != id {
		return fmt.Errorf("a folder named %q already exists", existing.Name)
	}
	res, err := s.db.Exec(`
UPDATE containers SET name=?, source_id=?, updated_at=? WHERE id=? AND kind=?`,
		name, sid, now(), id, KindFolder)
	if err != nil {
		return fmt.Errorf("renaming folder: %w", err)
	}
	return oneRow(res, "no folder #%d", id)
}

func (s *Store) RemoveFolder(id int64) error {
	res, err := s.db.Exec(`DELETE FROM containers WHERE id=? AND kind=?`, id, KindFolder)
	if err != nil {
		return fmt.Errorf("removing folder: %w", err)
	}
	return oneRow(res, "no folder #%d", id)
}

func oneRow(res sql.Result, format string, args ...any) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf(format, args...)
	}
	return nil
}

func (s *Store) FolderByRef(ref string) (*Container, error) {
	return s.containerByRef(KindFolder, "folder", ref)
}

func (s *Store) ListFolders() ([]DeckSummary, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name,
       COUNT(DISTINCT e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM containers ct
LEFT JOIN containers d ON d.parent_id = ct.id AND d.kind = '`+KindDeck+`'
    AND d.counted = 1
LEFT JOIN card_entries e ON e.container_id = d.id
LEFT JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinEntries+`
WHERE ct.kind = ?
GROUP BY ct.id
ORDER BY ct.name`, KindFolder)
	if err != nil {
		return nil, fmt.Errorf("listing folders: %w", err)
	}
	defer rows.Close()

	var out []DeckSummary
	for rows.Next() {
		var d DeckSummary
		d.Kind = KindFolder
		d.Source = "manual"
		d.Counted = true
		if err := rows.Scan(&d.ID, &d.Name,
			&d.DistinctCards, &d.TotalCopies, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MoveDeckToFolder(deckID, folderID int64) error {
	kind, err := s.containerKind(deckID)
	if err != nil {
		return err
	}
	if kind != KindDeck {
		return fmt.Errorf("only decks go in folders, and #%d is a %s", deckID, kind)
	}
	var parent any
	if folderID != 0 {
		target, err := s.containerKind(folderID)
		if err != nil {
			return err
		}
		if target != KindFolder {
			return fmt.Errorf("#%d is a %s, not a folder", folderID, target)
		}
		parent = folderID
	}
	res, err := s.db.Exec(`
UPDATE containers SET parent_id=?, updated_at=? WHERE id=?`, parent, now(), deckID)
	if err != nil {
		return fmt.Errorf("moving deck: %w", err)
	}
	return oneRow(res, "no deck #%d", deckID)
}

func (s *Store) containerKind(id int64) (string, error) {
	var kind string
	err := s.db.QueryRow(`SELECT kind FROM containers WHERE id=?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no container #%d", id)
	}
	return kind, err
}

func (s *Store) FolderByFinish(folderID int64) ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish, e.condition, NULL AS purchase_price,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN containers d ON d.id = e.container_id AND d.parent_id = ?
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
GROUP BY c.scryfall_id, e.finish, e.condition
ORDER BY value DESC, c.name`, folderID)
	if err != nil {
		return nil, fmt.Errorf("listing folder cards: %w", err)
	}
	return scanCollectionRows(rows)
}
