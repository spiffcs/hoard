package store

import (
	"path/filepath"
	"testing"
)

func TestCompendiumModeRoundTripsThroughSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.CompendiumMode() {
		t.Error("a fresh database reports catalog mode; it must default to off")
	}

	if err := s.SetCompendiumMode(true); err != nil {
		t.Fatalf("SetCompendiumMode: %v", err)
	}
	if !s.CompendiumMode() {
		t.Error("CompendiumMode() is false immediately after stamping")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { again.Close() })
	if !again.CompendiumMode() {
		t.Error("catalog mode did not survive a reopen; the guard must live in the file")
	}

	if err := again.SetCompendiumMode(false); err != nil {
		t.Fatalf("SetCompendiumMode(false): %v", err)
	}
	if again.CompendiumMode() {
		t.Error("catalog mode stayed on after being cleared")
	}
}
