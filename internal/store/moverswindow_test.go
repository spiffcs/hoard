package store

import "testing"

// holdFixture puts one copy of the fixture card in the collection in a given
// finish, with no price history of its own — these tests place every
// observation themselves, because Movers is a question about where a price sat
// on particular dates.
//
// Finishes stand in for separate printings here: Movers keys on card and
// finish together, so two finishes of one card are two independent series,
// which is all a test needs to watch one appear and another stay out.
func holdFixture(t *testing.T, s *Store, finish string) {
	t.Helper()
	if err := s.AddCardFinish(ulamog(), finish, 1); err != nil {
		t.Fatalf("AddCardFinish %s: %v", finish, err)
	}
}

// The reported defect. A window is a range, not a precondition: "--since 30d"
// asks what movement happened in the last thirty days, not which cards had a
// price thirty days ago. A printing first observed four days ago has four days
// of movement inside a thirty-day window, and that movement is the answer to
// the question rather than a degraded version of it.
//
// Observed on the owner's collection: the set hob holds fifteen printings, of
// which three reported at seven days and none at thirty or ninety. Belladonna
// Took had fallen from $35.50 to $3.85 inside its own record, and the wider
// windows — the ones that ask for more movement, not less — showed nothing.
func TestMoversReportsAMoveInsideAYoungerRecord(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, "nonfoil")
	// The whole record begins after the cutoff, and it moves a long way.
	observe(t, s, "nonfoil", 35.50, "2026-08-06T00:00:00Z")
	observe(t, s, "nonfoil", 3.85, "2026-08-10T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", "nonfoil")
	if c.Old != 35.50 || c.New != 3.85 {
		t.Errorf("moved %v -> %v, want 35.5 -> 3.85 measured from the start of its own record",
			c.Old, c.New)
	}
}

// The half of the original rule that was right, and has to stay right. One
// observation is not a change: there is nothing to compare it against, and
// reading a first price as a rise from nothing would put every newly priced
// card at the top of every list.
func TestMoversExcludesASinglePrice(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, "foil")
	observe(t, s, "foil", 25.00, "2026-08-06T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	for _, c := range got {
		if c.Finish == "foil" {
			t.Errorf("a printing priced exactly once reported as a mover: %+v", c)
		}
	}
}

// A window the history covers must still compare against the cutoff rather
// than the start of the record. This is the guard that the wider rule did not
// collapse into "always use the earliest price": with observations either side
// of the cutoff, the baseline is the last one at or before it.
func TestMoversPrefersTheCutoffBaselineOverTheRecordStart(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, "nonfoil")
	observe(t, s, "nonfoil", 5.00, "2026-06-01T00:00:00Z")
	observe(t, s, "nonfoil", 10.00, "2026-07-01T00:00:00Z")
	observe(t, s, "nonfoil", 12.00, "2026-08-10T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", "nonfoil")
	if c.Old != 10.00 {
		t.Errorf("baseline %v, want the 10.00 observed before the cutoff, not the record's first price",
			c.Old)
	}
}

// A series lying entirely before the cutoff has not moved inside the window,
// however much it moved before it: the baseline and the newest price are the
// same observation, and one observation is not a change.
func TestMoversExcludesASeriesThatEndsBeforeTheCutoff(t *testing.T) {
	s := newTestStore(t)
	holdFixture(t, s, "nonfoil")
	observe(t, s, "nonfoil", 5.00, "2026-06-01T00:00:00Z")
	observe(t, s, "nonfoil", 40.00, "2026-06-15T00:00:00Z")

	got, err := s.Movers("2026-07-11T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing: every observation predates the window", got)
	}
}
