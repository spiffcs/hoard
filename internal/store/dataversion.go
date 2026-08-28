package store

import "fmt"

func (s *Store) DataVersion() (int64, error) {
	if err := s.checkFile(); err != nil {
		return 0, err
	}
	var v int64
	if err := s.db.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading data_version: %w", err)
	}
	return v, nil
}
