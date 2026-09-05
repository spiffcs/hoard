package browse

import (
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func costLot(fin finish.Finish, qty int, paid *float64) store.Holding {
	return store.Holding{
		ContainerID: 7, ContainerName: "Trade", ContainerKind: store.KindDeck,
		Finish: fin, Condition: store.ConditionUnknown, Board: "main",
		Quantity: qty, PurchasePrice: paid,
		ScryfallID: "sol-" + fin.String(), SetCode: "c21", CollectorNumber: "263",
	}
}

func TestCostBasisWeightsByCopiesAndLeavesUncostedOut(t *testing.T) {
	got := costBasisByFinish([]store.Holding{
		costLot(finish.Nonfoil, 4, price(2)),
		costLot(finish.Nonfoil, 2, price(9)),
		costLot(finish.Nonfoil, 1, nil),
		costLot(finish.Foil, 1, price(40)),
	})

	nonfoil, ok := got[finish.Nonfoil]
	if !ok {
		t.Fatalf("no nonfoil cost basis in %+v", got)
	}
	if nonfoil.copies != 6 {
		t.Errorf("costed copies = %d, want 6: the uncosted copy must not be counted",
			nonfoil.copies)
	}
	if nonfoil.uncosted != 1 {
		t.Errorf("uncosted = %d, want 1", nonfoil.uncosted)
	}
	if want := 26.0 / 6.0; math.Abs(nonfoil.average()-want) > 1e-9 {
		t.Errorf("average = %v, want %v (weighted by copies, uncosted excluded)",
			nonfoil.average(), want)
	}

	foil, ok := got[finish.Foil]
	if !ok {
		t.Fatalf("no foil cost basis in %+v", got)
	}
	if foil.average() != 40 {
		t.Errorf("foil average = %v, want 40: finishes must not be blended",
			foil.average())
	}
}

func TestCostBasisIsEmptyWhenNothingHasARecordedCost(t *testing.T) {
	got := costBasisByFinish([]store.Holding{costLot(finish.Nonfoil, 3, nil)})

	cb := got[finish.Nonfoil]
	if cb.copies != 0 {
		t.Errorf("costed copies = %d, want 0", cb.copies)
	}
	if cb.uncosted != 3 {
		t.Errorf("uncosted = %d, want 3", cb.uncosted)
	}
	if cb.average() != 0 {
		t.Errorf("average = %v, want 0 and nothing rendered", cb.average())
	}
}

func TestDetailShowsTheAveragePaidAndHowManyCopiesItCovers(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			costLot(finish.Nonfoil, 4, price(2)),
			costLot(finish.Nonfoil, 2, price(9)),
			costLot(finish.Nonfoil, 1, nil),
		},
	}
	m := key(newTestModel(t, st), "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail opened")
	}

	view := m.detailView()
	if !strings.Contains(view, "$4.33") {
		t.Errorf("detail does not show the $4.33 average paid:\n%s", view)
	}
	if !strings.Contains(view, "6 of 7") {
		t.Errorf("detail does not say the average covers 6 of 7 copies:\n%s", view)
	}
}
