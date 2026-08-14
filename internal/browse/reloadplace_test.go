package browse

import (
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func manyCards(n int) []store.CollectionRow {
	out := make([]store.CollectionRow, 0, n)
	for i := range n {
		out = append(out, row(fmt.Sprintf("Card %03d", i), "uma", fmt.Sprintf("%d", i+1),
			finish.Nonfoil, 1, float64(n-i)))
	}
	return out
}

type spot struct {
	cardCursor  int
	cardOffset  int
	cardsPage   int
	selected    string
	contCursor  int
	contOffset  int
	focus       pane
	filterText  string
	floorIdx    int
	watchOffset [watchSectionCount]int
}

func spotOf(m Model) spot {
	p := spot{
		cardCursor: m.cursor[paneCards], cardOffset: m.offset[paneCards],
		cardsPage:  m.cardsPage,
		contCursor: m.cursor[paneContainers], contOffset: m.offset[paneContainers],
		focus: m.focus, filterText: m.filterText, floorIdx: m.floorIdx,
		watchOffset: m.watchSecOffset,
	}
	if c := m.selectedCard(); c != nil {
		p.selected = c.Name
	}
	return p
}

func deepInThePane(t *testing.T) Model {
	t.Helper()
	st := testStore()
	st.collection = manyCards(130)
	m := newTestModel(t, st)
	m.focus = paneCards
	m.cardsPage = 1
	m.deriveCardsPage()
	m.cursor[paneCards] = 12
	m.offset[paneCards] = 8
	return m
}

func TestReloadKeepsItsPromise(t *testing.T) {
	m := deepInThePane(t)
	before := spotOf(m)
	if before.selected == "" {
		t.Fatal("fixture put the cursor on no row")
	}

	after := spotOf(key(m, "r"))

	if after.selected != before.selected {
		t.Errorf("cursor moved to a different card: %q -> %q", before.selected, after.selected)
	}
	if after.cardCursor != before.cardCursor {
		t.Errorf("card cursor %d -> %d", before.cardCursor, after.cardCursor)
	}
	if after.cardsPage != before.cardsPage {
		t.Errorf("page %d -> %d", before.cardsPage, after.cardsPage)
	}
	if after.cardOffset != before.cardOffset {
		t.Errorf("scroll offset %d -> %d", before.cardOffset, after.cardOffset)
	}
}

func TestReloadAndRefreshAgree(t *testing.T) {
	viaReload := spotOf(key(deepInThePane(t), "r"))

	m := deepInThePane(t)
	m.refresh()
	viaRefresh := spotOf(m)

	if viaReload != viaRefresh {
		t.Errorf("the two re-read paths disagree:\n reload  %+v\n refresh %+v",
			viaReload, viaRefresh)
	}
}

func TestReloadKeepsTheRestOfThePlace(t *testing.T) {
	st := testStore()
	st.collection = manyCards(130)
	m := newTestModel(t, st)
	m.focus = paneCards
	m.floorIdx = 1
	m.filterText = "card 01"
	m.setFilter(m.filterText)
	m.cursor[paneContainers] = 1

	before := spotOf(m)
	after := spotOf(key(m, "r"))

	if after.filterText != before.filterText {
		t.Errorf("filter text %q -> %q", before.filterText, after.filterText)
	}
	if after.floorIdx != before.floorIdx {
		t.Errorf("floor %d -> %d", before.floorIdx, after.floorIdx)
	}
	if after.contCursor != before.contCursor {
		t.Errorf("container cursor %d -> %d", before.contCursor, after.contCursor)
	}
	if after.focus != before.focus {
		t.Errorf("focus %v -> %v", before.focus, after.focus)
	}
	if len(m.filteredCards) == 0 {
		t.Fatal("the filter matched nothing, so nothing was proven")
	}
}

func TestReloadUnreachableFromTheDetailOverlay(t *testing.T) {
	if detailPaletteIDs["reload"] {
		t.Error("the palette offers reload over the detail overlay")
	}
	m := newTestModel(t, testStore())
	m.focus = paneCards
	m.openDetail()
	if m.detail == nil {
		t.Fatal("the fixture opened no overlay")
	}
	if m.mode() != modeDetail {
		t.Fatalf("mode is %v, not modeDetail", m.mode())
	}
	before := m.detail.card.Name
	after := key(m, "r")
	if after.detail == nil {
		t.Fatal("r closed the overlay")
	}
	if after.detail.card.Name != before {
		t.Errorf("r re-pointed the overlay: %q -> %q", before, after.detail.card.Name)
	}
	if after.status == "reloaded" {
		t.Error("r ran a reload from inside the overlay")
	}
}
