package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func deckEditModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Llanowar Elves", "main", finish.Nonfoil, 1, 1),
		entry("Sol Ring", "side", finish.Nonfoil, 2, 2),
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.focus = paneCards
	return m, f
}

func cardCursorOn(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, c := range m.cards {
		if c.Name == name {
			m.cursor[paneCards] = i
			return m
		}
	}
	t.Fatalf("no card row for %q", name)
	return m
}

func deckQuantity(t *testing.T, f *fakeStore, deckID int64, name, board string) int {
	t.Helper()
	for _, e := range f.deckCards[deckID] {
		if e.Card.Name == name && e.Board == board {
			return e.Quantity
		}
	}
	t.Fatalf("no %q on board %q in deck %d", name, board, deckID)
	return 0
}

func TestPlusRaisesTheQuantityOfACardInADeck(t *testing.T) {
	m, f := deckEditModel(t)
	m = cardCursorOn(t, m, "Llanowar Elves")

	m = key(m, "+")

	if m.statusErr {
		t.Fatalf("editing a deck card was refused: %q", m.status)
	}
	if got := deckQuantity(t, f, 201, "Llanowar Elves", "main"); got != 2 {
		t.Errorf("Llanowar Elves ×%d in the deck, want 2", got)
	}
}

func TestEditingASideboardCardDoesNotDisturbTheMainDeck(t *testing.T) {
	m, f := deckEditModel(t)
	m = cardCursorOn(t, m, "Sol Ring")

	m = key(m, "+")

	if m.statusErr {
		t.Fatalf("editing a sideboard card was refused: %q", m.status)
	}
	if got := deckQuantity(t, f, 201, "Sol Ring", "side"); got != 3 {
		t.Errorf("Sol Ring ×%d in the sideboard, want 3", got)
	}
	if got := deckQuantity(t, f, 201, "Llanowar Elves", "main"); got != 1 {
		t.Errorf("the main deck changed too: Llanowar Elves ×%d, want 1", got)
	}
	if n := len(f.deckCards[201]); n != 2 {
		t.Errorf("the deck now has %d rows, want 2 — the edit forked a row", n)
	}
}

func TestMinusRemovesTheLastCopyFromADeck(t *testing.T) {
	m, f := deckEditModel(t)
	m = cardCursorOn(t, m, "Llanowar Elves")

	m = key(m, "-")

	if m.statusErr {
		t.Fatalf("removing a deck card was refused: %q", m.status)
	}
	for _, e := range f.deckCards[201] {
		if e.Card.Name == "Llanowar Elves" {
			t.Errorf("Llanowar Elves is still in the deck ×%d", e.Quantity)
		}
	}
}

func TestADeckCardIsEditableFromTheCardDetail(t *testing.T) {
	m, _ := deckEditModel(t)
	m.detail = &detail{holdings: []store.Holding{{
		ContainerID: 201, ContainerName: "Cheap Deck", ContainerKind: store.KindDeck,
		ScryfallID: "Llanowar Elves-id", Finish: finish.Nonfoil,
		Condition: store.ConditionUnknown, Board: "main", Quantity: 1,
	}}}

	h, ok := m.heldEditable()
	if !ok {
		t.Fatalf("a deck holding is not editable from the detail view: %q", m.status)
	}
	if h.Board != "main" {
		t.Errorf("editable holding lost its board: %+v", h)
	}
}

func TestMovingADeckCardToABinderIsStillRefused(t *testing.T) {
	m, _ := deckEditModel(t)
	m.detail = &detail{holdings: []store.Holding{{
		ContainerID: 201, ContainerName: "Cheap Deck", ContainerKind: store.KindDeck,
		ScryfallID: "Llanowar Elves-id", Finish: finish.Nonfoil,
		Condition: store.ConditionUnknown, Board: "main", Quantity: 1,
	}}}

	m.promptHeldLocation()

	if m.prompt != nil {
		t.Fatal("the detail view offered to move a deck card into a binder")
	}
	if !m.statusErr || !strings.Contains(m.status, "deck") {
		t.Errorf("status = %q, want it to say why a deck card cannot be moved here", m.status)
	}
}
