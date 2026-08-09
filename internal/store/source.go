package store

// Reading another hoard's database without writing to it.
//
// Open() is the wrong door for this: it migrates as a side effect, and even at
// a matching version it creates the singleton collection when one is missing.
// Both are right for the database hoard is working in and wrong for one it has
// merely been pointed at, where the file is the user's other copy and a read
// must leave it byte-identical.

import (
	"database/sql"
	"fmt"
	"os"
)

// FileVersion reports the schema version stamped on the database at path,
// without migrating it and without creating it.
//
// The existence check is load-bearing rather than a nicety: SQLite creates a
// database on open, so a typo'd path would otherwise leave an empty file
// behind and report version 0 — indistinguishable from a real hoard predating
// versioning.
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

// OpenSource opens a database for reading only. Every write on the returned
// Store fails at the engine, so "this never touched the other hoard" is a
// guarantee the database enforces rather than a promise the caller keeps.
//
// It refuses a version mismatch instead of migrating, because migrating is a
// write. A caller holding an older database must upgrade it deliberately —
// see action.MergeHoard, which asks first.
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

// openReadOnly dials a connection that cannot write. query_only makes SQLite
// itself reject a write, so a mistake surfaces as an error on the statement
// rather than as a modified file. foreign_keys is left off: nothing here
// writes, and the pragma only governs writes.
func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite",
		path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}
	// One connection, for the reason Open gives: a pragma must land on the
	// same connection as the statements it configures.
	db.SetMaxOpenConns(1)
	return db, nil
}
