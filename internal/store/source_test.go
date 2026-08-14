package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	v, err := FileVersion(path)
	if err != nil {
		t.Fatalf("FileVersion: %v", err)
	}
	if v != SchemaVersion() {
		t.Errorf("FileVersion = %d, want %d", v, SchemaVersion())
	}

	missing := filepath.Join(t.TempDir(), "absent.db")
	if _, err := FileVersion(missing); err == nil {
		t.Error("FileVersion accepted a path with no database")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("FileVersion created the database it was only asked about")
	}
}

func TestOpenSourceRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	src, err := OpenSource(path)
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	defer src.Close()

	if _, err := src.CreateBinder("Trades"); err == nil {
		t.Error("a write succeeded on a read-only handle")
	}

	if _, err := src.ListBinders(); err != nil {
		t.Errorf("ListBinders on a read-only handle: %v", err)
	}
	if _, err := src.Snapshot(); err != nil {
		t.Errorf("Snapshot on a read-only handle: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the database changed while open read-only")
	}
}

func TestOpenSourceRefusesVersionMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatalf("stamping an old version: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := OpenSource(path); err == nil {
		t.Fatal("OpenSource accepted a database at a different schema")
	}
	if v, verr := FileVersion(path); verr != nil || v != 2 {
		t.Errorf("version is now %d (err %v); the refusal must not have migrated it", v, verr)
	}
}
