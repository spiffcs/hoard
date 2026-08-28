package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAHoardNoticesItsDatabaseWasRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.DataVersion(); err != nil {
		t.Fatalf("DataVersion on a healthy database: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database: %v", err)
	}

	_, err = s.DataVersion()
	if !errors.Is(err, ErrDatabaseReplaced) {
		t.Fatalf("DataVersion after the file was removed = %v, want ErrDatabaseReplaced", err)
	}
}

func TestAHoardNoticesItsDatabaseWasReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	watcher, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer watcher.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database: %v", err)
	}
	rebuilt, err := Open(path)
	if err != nil {
		t.Fatalf("Open (rebuild): %v", err)
	}
	defer rebuilt.Close()

	_, err = watcher.DataVersion()
	if !errors.Is(err, ErrDatabaseReplaced) {
		t.Fatalf("DataVersion after a rebuild took the path = %v, want ErrDatabaseReplaced", err)
	}
}

func TestTwoHoardsOnOneDatabaseBothStayLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	mine, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer mine.Close()
	theirs, err := Open(path)
	if err != nil {
		t.Fatalf("Open (second hoard): %v", err)
	}
	defer theirs.Close()

	if _, err := theirs.CreateBinder("theirs"); err != nil {
		t.Fatalf("CreateBinder on the second hoard: %v", err)
	}
	if _, err := mine.DataVersion(); err != nil {
		t.Fatalf("DataVersion while sharing one file: %v, want the watcher to stay live", err)
	}
}
