package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func TestViewCyclePutsDipBetweenMarketAndWatches(t *testing.T) {
	if got := viewMarket.next(); got != viewDip {
		t.Errorf("market.next() = %v, want dip", got)
	}
	if got := viewDip.next(); got != viewWatches {
		t.Errorf("dip.next() = %v, want watches", got)
	}
	if got := viewDip.String(); got == "" || got == "holdings" {
		t.Errorf("dip.String() = %q, want its own name", got)
	}
}

func dipRow(name string, high, low, last float64) store.TrendRow {
	return store.TrendRow{
		ScryfallID: name, Name: name, SetCode: "tst", CollectorNumber: "1",
		Finish: finish.Nonfoil, Copies: 1,
		First: high, Last: last, Low: low, High: high,
	}
}

func momentumRow(name string, first, last float64, ups int) store.TrendRow {
	return store.TrendRow{
		ScryfallID: name, Name: name, SetCode: "tst", CollectorNumber: "2",
		Finish: finish.Foil, Copies: 1,
		First: first, Last: last, Low: first, High: last,
		Ups: ups, Moves: ups,
	}
}

func TestDipViewRendersBothTables(t *testing.T) {
	f := testStore()
	f.dips = []store.TrendRow{dipRow("Jeweled Lotus", 92.20, 75.34, 75.34)}
	f.momentum = []store.TrendRow{momentumRow("Command Tower", 28.28, 82.76, 19)}

	m := atAllCards(t, newTestModel(t, f))
	m.view = viewDip
	if err := m.loadView(); err != nil {
		t.Fatalf("loadView: %v", err)
	}

	body := strings.Join(m.dipLines(100), "\n")

	for _, want := range []string{"DIP", "MOMENTUM", "Jeweled Lotus", "Command Tower"} {
		if !strings.Contains(body, want) {
			t.Errorf("dip view is missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "19") {
		t.Errorf("momentum table does not show the streak length:\n%s", body)
	}
}

func TestDipViewSaysWhenASectionIsEmpty(t *testing.T) {
	f := testStore()
	f.dips = nil
	f.momentum = []store.TrendRow{momentumRow("Command Tower", 28.28, 82.76, 19)}

	m := atAllCards(t, newTestModel(t, f))
	m.view = viewDip
	if err := m.loadView(); err != nil {
		t.Fatalf("loadView: %v", err)
	}

	body := strings.Join(m.dipLines(100), "\n")
	if !strings.Contains(body, "DIP") || !strings.Contains(body, "MOMENTUM") {
		t.Fatalf("both headings must show even when one is empty:\n%s", body)
	}
	if !strings.Contains(body, "Command Tower") {
		t.Errorf("the populated section vanished:\n%s", body)
	}
}

func (f *fakeStore) Dips(store.TrendOptions) ([]store.TrendRow, error) {
	f.dipCalls++
	return f.dips, f.err
}

func (f *fakeStore) Momentum(store.TrendOptions) ([]store.TrendRow, error) {
	f.momentumCalls++
	return f.momentum, f.err
}
