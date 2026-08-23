package store

import (
	"database/sql"
	"fmt"
)

const catalogModeKey = "catalog.mode"

func (s *Store) CatalogMode() bool { return s.catalog }

func (s *Store) SetCatalogMode(on bool) error {
	if !on {
		if _, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, catalogModeKey); err != nil {
			return fmt.Errorf("clearing catalog mode: %w", err)
		}
		s.catalog = false
		return nil
	}
	if err := s.SaveSettings(map[string]string{catalogModeKey: "true"}); err != nil {
		return err
	}
	s.catalog = true
	return nil
}

func (s *Store) loadCatalogMode() error {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, catalogModeKey).Scan(&v)
	if err == sql.ErrNoRows {
		s.catalog = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading catalog mode: %w", err)
	}
	s.catalog = v == "true"
	return nil
}
