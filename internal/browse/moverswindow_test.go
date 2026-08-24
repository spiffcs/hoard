package browse

import (
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

var pinnedNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func windowStore(covered, started string) *fakeStore {
	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "old-id", Name: "Longrecord", SetCode: "aaa", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 1, Old: 10, New: 20, OldAsOf: covered},
		{ScryfallID: "new-id", Name: "Shortrecord", SetCode: "bbb", CollectorNumber: "2",
			Finish: finish.Nonfoil, Copies: 1, Old: 50, New: 5, OldAsOf: started},
	}
	return st
}

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

	if unwanted := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC).Local().Format("2 Jan"); strings.Contains(out, unwanted) {
		t.Errorf("dated a row the window already covers (%q):\n%s", unwanted, out)
	}
}

func TestMoversPaneHidesTheColumnWhenTheWindowIsCovered(t *testing.T) {
	m := onWindowMovers(t, windowStore("2026-06-20T12:00:00Z", "2026-06-25T12:00:00Z"))
	out := m.View()

	if strings.Contains(out, "FROM") {
		t.Errorf("FROM column rendered with nothing to say on any row:\n%s", out)
	}

	for _, name := range []string{"Longrecord", "Shortrecord"} {
		if !strings.Contains(out, name) {
			t.Errorf("%q missing from the pane:\n%s", name, out)
		}
	}
}
