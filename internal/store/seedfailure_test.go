package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedingSaysWhatFailedAndWhereWhenTheCommitFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		t.Fatalf("deferring foreign keys: %v", err)
	}

	_, _, err = s.SeedCompendiumPrintings(999999, catalogPrintings())
	if err == nil {
		t.Fatal("seeding into a container that does not exist succeeded")
	}
	if !strings.Contains(err.Error(), "seeding") {
		t.Errorf("commit failure %q does not say what was being done", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("commit failure %q does not name the database", err)
	}
}
