package store

import (
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

func sinkingStore(t *testing.T) *Store {
	t.Helper()
	return trendStore(t, map[string][]mtgjson.Observation{
		"dip-id": daily(10, 9, 8, 7),
	}, trendCard("dip-id", "Sinking", "2020-01-01"))
}

func slidingOptions() TrendOptions {
	return TrendOptions{Since: "2026-06-01T00:00:00Z", MinChecks: 2}
}

func TestTrendReadsDoNotQueueBehindTheMainConnection(t *testing.T) {
	s := sinkingStore(t)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback()

	done := make(chan error, 1)
	go func() {
		_, err := s.Dips(slidingOptions())
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Dips while the main connection was busy: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dips queued behind the busy main connection instead of reading alongside it")
	}
}

func TestMomentumReadsDoNotQueueBehindTheMainConnection(t *testing.T) {
	s := sinkingStore(t)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback()

	done := make(chan error, 1)
	go func() {
		_, err := s.Momentum(slidingOptions())
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Momentum while the main connection was busy: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Momentum queued behind the busy main connection instead of reading alongside it")
	}
}

func TestClosingTheStoreClosesTheBackgroundReader(t *testing.T) {
	s := sinkingStore(t)
	if _, err := s.Dips(slidingOptions()); err != nil {
		t.Fatalf("Dips: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Dips(slidingOptions()); err == nil {
		t.Fatal("Dips answered after Close; the background reader outlived the store")
	}
}
