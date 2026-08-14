package store

import (
	"database/sql"
	"fmt"
	"os"
)

func FileVersion(path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, fmt.Errorf("reading database %q: %w", path, err)
	}
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading schema version of %q: %w", path, err)
	}
	return v, nil
}

func OpenSource(path string) (*Store, error) {
	v, err := FileVersion(path)
	if err != nil {
		return nil, err
	}
	if v != schemaVersion {
		return nil, fmt.Errorf(
			"database %q is schema v%d but this hoard understands v%d; it must be upgraded before it can be read",
			path, v, schemaVersion)
	}
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite",
		path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	return db, nil
}
