package browse

import (
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

// pinnedNow is the instant the window tests measure back from, so the 30-day
// default window opens on 12 Jul 2026 whenever the suite runs.
var pinnedNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

// windowStore seeds one row whose record reaches back past the window's start
// and one whose record begins inside it — the two cases the FROM column exists
// to tell apart.
func windowStore(covered, started string) *fakeStore {
	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "old-id", Name: "Longrecord", SetCode: "aaa", CollectorNumber: "1",
			Finish: "nonfoil", Copies: 1, Old: 10, New: 20, OldAsOf: covered},
		{ScryfallID: "new-id", Name: "Shortrecord", SetCode: "bbb", CollectorNumber: "2",
			Finish: "nonfoil", Copies: 1, Old: 50, New: 5, OldAsOf: started},
	}
	return st
}

// onWindowMovers puts the model on the movers view with the clock pinned, so
// the window's start is a fixed date rather than whenever the test ran.
func onWindowMovers(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m := newTestModel(t, st)
	m.clock = func() time.Time { return pinnedNow }
	m = atAllCards(t, m)
	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	return m
}

// A row measuring less time than the window says where its own measurement
// starts, so two rows in one table can be told apart. The default window is 30
// days: Shortrecord's record begins nine days before the pinned now, well
// inside it.
func TestMoversPaneDatesARowThatStartsInsideTheWindow(t *testing.T) {
	m := onWindowMovers(t, windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z"))
	out := m.View()

	if !strings.Contains(out, "FROM") {
		t.Errorf("no FROM column for a row measuring less than the window:\n%s", out)
	}
	want := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan")
	if !strings.Contains(out, want) {
		t.Errorf("row does not name its own start %q:\n%s", want, out)
	}
	// The row the window covers stays blank: its figures are the window's, and
	// a date repeating the header would be a column of noise.
	if unwanted := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan"); strings.Contains(out, unwanted) {
		t.Errorf("dated a row the window already covers (%q):\n%s", unwanted, out)
	}
}

// The other half, and the one that keeps the ordinary pane exactly as it was:
// with every baseline inside the record the column has nothing to say on any
// row, so it must not take a column of width from the card names.
func TestMoversPaneHidesTheColumnWhenTheWindowIsCovered(t *testing.T) {
	m := onWindowMovers(t, windowStore("2026-06-20T12:00:00Z", "2026-06-25T12:00:00Z"))
	out := m.View()

	if strings.Contains(out, "FROM") {
		t.Errorf("FROM column rendered with nothing to say on any row:\n%s", out)
	}
	// Both rows still render: hiding the column is a display decision and must
	// not be mistaken for hiding the rows.
	for _, name := range []string{"Longrecord", "Shortrecord"} {
		if !strings.Contains(out, name) {
			t.Errorf("%q missing from the pane:\n%s", name, out)
		}
	}
}
