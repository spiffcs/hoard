package store

import (
	"path/filepath"
	"testing"
)

func TestCatalogModeRoundTripsThroughSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.CatalogMode() {
		t.Error("a fresh database reports catalog mode; it must default to off")
	}

	if err := s.SetCatalogMode(true); err != nil {
		t.Fatalf("SetCatalogMode: %v", err)
	}
	if !s.CatalogMode() {
		t.Error("CatalogMode() is false immediately after stamping")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { again.Close() })
	if !again.CatalogMode() {
		t.Error("catalog mode did not survive a reopen; the guard must live in the file")
	}

	if err := again.SetCatalogMode(false); err != nil {
		t.Fatalf("SetCatalogMode(false): %v", err)
	}
	if again.CatalogMode() {
		t.Error("catalog mode stayed on after being cleared")
	}
}
