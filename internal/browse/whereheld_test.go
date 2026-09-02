package browse

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func whereStore() *fakeStore {
	f := testStore()
	f.deckCards = map[int64][]store.EntryView{
		201: {entry("Sol Ring", "main", finish.Nonfoil, 1, 10)},
		202: {entry("Solitude", "main", finish.Nonfoil, 1, 34)},
	}
	return f
}

func cardRow(t *testing.T, m Model, name string) string {
	t.Helper()
	body := strings.Join(m.cardLines(120), "\n")
	return strings.TrimRight(ansi.Strip(lineFor(t, body, name)), " ")
}

func TestAllCardsAddsAWhereColumnAfterValue(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	head := ansi.Strip(m.cardLines(120)[0])
	where := strings.Index(head, "WHERE")
	if where < 0 {
		t.Fatalf("header = %q, want a WHERE column naming the binder or deck", head)
	}
	if value := strings.Index(head, "VALUE"); where < value {
		t.Errorf("header = %q, want WHERE to the right of VALUE", head)
	}
}

func TestAllCardsRowsEndWithTheContainerHoldingThem(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	for _, tc := range []struct{ card, want string }{
		{"Bitterblossom", store.LooseName},
		{"Solitude", "Rich Deck"},
	} {
		if got := cardRow(t, m, tc.card); !strings.HasSuffix(got, tc.want) {
			t.Errorf("%s row = %q, want it to end in %q", tc.card, got, tc.want)
		}
	}
}

func TestAllCardsRowHeldInTwoPlacesCountsTheRest(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	want := store.LooseName + " +1"
	if got := cardRow(t, m, "Sol Ring"); !strings.HasSuffix(got, want) {
		t.Errorf("Sol Ring row = %q, want it to end in %q — one row merges %s and Cheap Deck",
			got, want, store.LooseName)
	}
}

func TestBinderViewHasNoWhereColumn(t *testing.T) {
	m := newTestModel(t, whereStore())

	if head := ansi.Strip(m.cardLines(120)[0]); strings.Contains(head, "WHERE") {
		t.Errorf("header = %q, want no WHERE column: every row in a binder is in that binder", head)
	}
}
