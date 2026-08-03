package store

// The settings table: small key-value preferences that should survive a
// restart — the penny filters' state first. Preferences, not data: losing
// this table costs a toggle, never a card.

import "fmt"

// Settings returns every stored preference. The table is a handful of
// rows, so it reads whole.
func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SaveSettings upserts the given preferences, leaving others untouched.
func (s *Store) SaveSettings(kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, v := range kv {
		if _, err := stmt.Exec(k, v); err != nil {
			return fmt.Errorf("saving setting %s: %w", k, err)
		}
	}
	return tx.Commit()
}
