package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestABuildRefusesWhileAnotherHoardHoldsTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	watcher, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer watcher.Close()

	built, err := OpenExclusive(path)
	if err == nil {
		built.Close()
		t.Fatal("OpenExclusive started a build on a database another hoard has open")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("refusal %q does not name the database", err)
	}
}

func TestABuildRefusesWhenAStaleHoardStillHoldsThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	watcher, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer watcher.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database: %v", err)
	}

	built, err := OpenExclusive(path)
	if err == nil {
		built.Close()
		t.Fatal("OpenExclusive started a build on a path a stale hoard still holds")
	}
}

func TestABuildProceedsOnceTheOtherHoardCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.db")
	watcher, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database: %v", err)
	}

	built, err := OpenExclusive(path)
	if err != nil {
		t.Fatalf("OpenExclusive after the other hoard closed: %v", err)
	}
	defer built.Close()
}

func TestTheBuildLockIsNotSweptAwayWithTheDatabaseFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "all.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	matches, err := filepath.Glob(filepath.Join(dir, "all.db*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			t.Fatalf("removing %s: %v", m, err)
		}
	}

	built, err := OpenExclusive(path)
	if err == nil {
		built.Close()
		t.Fatal("rm all.db* swept away the lock, so a build started under a live hoard")
	}
}
