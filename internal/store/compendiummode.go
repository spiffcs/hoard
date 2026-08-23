package store

import (
	"database/sql"
	"fmt"
)

const compendiumModeKey = "compendium.mode"

func (s *Store) CompendiumMode() bool { return s.compendium }

func (s *Store) SetCompendiumMode(on bool) error {
	if !on {
		if _, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, compendiumModeKey); err != nil {
			return fmt.Errorf("clearing compendium mode: %w", err)
		}
		s.compendium = false
		return nil
	}
	if err := s.SaveSettings(map[string]string{compendiumModeKey: "true"}); err != nil {
		return err
	}
	s.compendium = true
	return nil
}

func (s *Store) loadCompendiumMode() error {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, compendiumModeKey).Scan(&v)
	if err == sql.ErrNoRows {
		s.compendium = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading compendium mode: %w", err)
	}
	s.compendium = v == "true"
	return nil
}
