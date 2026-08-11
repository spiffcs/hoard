package store

import (
	"path/filepath"
	"testing"
)

// TestDataVersionSeesOnlyOtherConnections pins the three properties the
// browser's live refresh is built on: our own reads and our own writes leave
// the counter alone, and another connection's commit moves it. Lose the
// middle one and the browser refreshes itself in a loop every time the user
// edits a card.
func TestDataVersionSeesOnlyOtherConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	mine, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer mine.Close()
	// Opened up front so its own migration and singleton-collection writes
	// land before the baseline, not inside the measurement.
	theirs, err := Open(path)
	if err != nil {
		t.Fatalf("Open (second connection): %v", err)
	}
	defer theirs.Close()

	v0, err := mine.DataVersion()
	if err != nil {
		t.Fatalf("DataVersion: %v", err)
	}

	if _, err := mine.ListBinders(); err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if v, err := mine.DataVersion(); err != nil || v != v0 {
		t.Errorf("after our own read: %d (err %v), want %d", v, err, v0)
	}

	if _, err := mine.CreateBinder("ours"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if v, err := mine.DataVersion(); err != nil || v != v0 {
		t.Errorf("after our own write: %d (err %v), want %d — the browser "+
			"would chase its own edits", v, err, v0)
	}

	if _, err := theirs.CreateBinder("theirs"); err != nil {
		t.Fatalf("CreateBinder on the second connection: %v", err)
	}
	v3, err := mine.DataVersion()
	if err != nil {
		t.Fatalf("DataVersion: %v", err)
	}
	if v3 == v0 {
		t.Errorf("after another connection's write: still %d — the counter "+
			"does not detect the thing it is being used to detect", v3)
	}
}
