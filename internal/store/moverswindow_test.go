package store

import "github.com/spiffcs/hoard/internal/finish"

import "testing"

func holdFixture(t *testing.T, s *Store, fin finish.Finish) {
	t.Helper()
	if err := s.AddCardFinish(ulamog(), fin, 1); err != nil {
		t.Fatalf("AddCardFinish %s: %v", fin, err)
	}
}

func TestMoversReportsAMoveInsideAYoungerRecord(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, finish.Nonfoil)

	observe(t, s, finish.Nonfoil, 35.50, "2026-08-06T00:00:00Z")
	observe(t, s, finish.Nonfoil, 3.85, "2026-08-10T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", finish.Nonfoil)
	if c.Old != 35.50 || c.New != 3.85 {
		t.Errorf("moved %v -> %v, want 35.5 -> 3.85 measured from the start of its own record",
			c.Old, c.New)
	}
}

func TestMoversExcludesASinglePrice(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, finish.Foil)
	observe(t, s, finish.Foil, 25.00, "2026-08-06T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	for _, c := range got {
		if c.Finish == finish.Foil {
			t.Errorf("a printing priced exactly once reported as a mover: %+v", c)
		}
	}
}

func TestMoversPrefersTheCutoffBaselineOverTheRecordStart(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, finish.Nonfoil)
	observe(t, s, finish.Nonfoil, 5.00, "2026-06-01T00:00:00Z")
	observe(t, s, finish.Nonfoil, 10.00, "2026-07-01T00:00:00Z")
	observe(t, s, finish.Nonfoil, 12.00, "2026-08-10T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", finish.Nonfoil)
	if c.Old != 10.00 {
		t.Errorf("baseline %v, want the 10.00 observed before the cutoff, not the record's first price",
			c.Old)
	}
}

func TestMoversExcludesASeriesThatEndsBeforeTheCutoff(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, finish.Nonfoil)
	observe(t, s, finish.Nonfoil, 5.00, "2026-06-01T00:00:00Z")
	observe(t, s, finish.Nonfoil, 40.00, "2026-06-15T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing: every observation predates the window", got)
	}
}
