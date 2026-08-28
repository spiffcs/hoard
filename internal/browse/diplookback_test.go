package browse

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

func dipLookbackStore() *fakeStore {
	st := testStore()
	st.dips = []store.TrendRow{dipRow("Jeweled Lotus", 92.20, 75.34, 75.34)}
	st.momentum = []store.TrendRow{momentumRow("Command Tower", 28.28, 82.76, 19)}
	return st
}

func onDipView(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m := newTestModel(t, st)
	m.clock = func() time.Time { return pinnedNow }
	m = atAllCards(t, m)
	for range 3 {
		m = pumpKey(t, m, "v")
	}
	if m.view != viewDip {
		t.Fatalf("view = %v, want dip", m.view)
	}
	return m
}

func trendDaysAsked(t *testing.T, st *fakeStore) []int {
	t.Helper()
	var days []int
	for _, o := range st.trendOpts {
		since, err := time.Parse(time.RFC3339, o.Since)
		if err != nil {
			t.Fatalf("Since %q is not a timestamp: %v", o.Since, err)
		}
		d := int(math.Floor(pinnedNow.Sub(since).Hours() / 24))
		if !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	return days
}

func TestDipViewCyclesTheLookbackWithW(t *testing.T) {
	st := dipLookbackStore()
	m := onDipView(t, st)

	if got := trendDaysAsked(t, st); !slices.Equal(got, []int{30}) {
		t.Fatalf("dip view opened on lookback %v days, want 30", got)
	}

	for _, want := range []int{90, 7, 30} {
		m = pumpKey(t, m, "W")
		if !strings.Contains(m.status, fmt.Sprintf("%d days", want)) {
			t.Errorf("W did not report a %d day lookback; status = %q", want, m.status)
		}
	}

	asked := trendDaysAsked(t, st)
	for _, want := range []int{7, 30, 90} {
		if !slices.Contains(asked, want) {
			t.Errorf("the store was never asked for a %d day lookback; asked %v", want, asked)
		}
	}
}

func TestDipLookbackKeepsShowingBothTablesAfterW(t *testing.T) {
	m := pumpKey(t, onDipView(t, dipLookbackStore()), "W")

	body := strings.Join(m.dipLines(100), "\n")
	for _, want := range []string{"DIP", "MOMENTUM", "Jeweled Lotus", "Command Tower"} {
		if !strings.Contains(body, want) {
			t.Errorf("after W the dip view is missing %q:\n%s", want, body)
		}
	}
}

func TestDipLookbackNeverStopsOnCostBasis(t *testing.T) {
	st := dipLookbackStore()
	st.costBasis = costBasisRows()
	m := onDipView(t, st)

	for range 8 {
		m = pumpKey(t, m, "W")
		if namesCostBasis(m.status) {
			t.Fatalf("the dip lookback offered a cost basis stop: %q", m.status)
		}
	}
	if asked := trendDaysAsked(t, st); !slices.Contains(asked, 90) {
		t.Errorf("cycling never reached the 90 day lookback; asked %v", asked)
	}
}
