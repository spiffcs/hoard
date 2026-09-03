package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

// peelDeckModel gives the main deck three cards of one copy each, so peeling
// one off the board leaves cards on either side of where the cursor was.
func peelDeckModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
		entry("Sol Ring", "main", finish.Nonfoil, 1, 50),
		entry("Llanowar Elves", "main", finish.Nonfoil, 1, 10),
		entry("Pyroblast", "side", finish.Nonfoil, 1, 75),
	}
	return deckModelFor(t, f)
}

func deckModelFor(t *testing.T, f *fakeStore) (Model, *fakeStore) {
	t.Helper()
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

func cursorCard(t *testing.T, m Model) card {
	t.Helper()
	c := m.selectedCard()
	if c == nil {
		t.Fatal("no card under the cursor")
	}
	return *c
}

func wantCursor(t *testing.T, m Model, name, board string) {
	t.Helper()
	c := cursorCard(t, m)
	if c.Name != name || boardKey(c.Board) != board {
		t.Errorf("cursor is on %s (%s board), want %s (%s board)",
			c.Name, boardKey(c.Board), name, board)
	}
}

// The whole point: the cursor keeps working the board it was working, so a
// run of presses peels card after card off the same board.
func TestBLeavesTheCursorOnTheNextCardOfTheSameBoard(t *testing.T) {
	m, _ := peelDeckModel(t)
	m = cardCursorOn(t, m, "Sol Ring")

	m = key(m, "b")

	if m.statusErr {
		t.Fatalf("b was refused: %q", m.status)
	}
	wantCursor(t, m, "Llanowar Elves", "main")
}

func TestBPeelsARunOfCardsOffOneBoardWithoutChasingThem(t *testing.T) {
	m, f := peelDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "b")
	m = key(m, "b")

	for _, name := range []string{"Force of Will", "Sol Ring"} {
		if got := boardOf(t, f, 201, name); got != "side" {
			t.Errorf("%s is on the %s board, want side", name, got)
		}
	}
	wantCursor(t, m, "Llanowar Elves", "main")
}

// The last card of a board has nothing under it, so the cursor steps back
// rather than falling into the board below.
func TestBStepsBackWhenItPeelsTheLastCardOfABoard(t *testing.T) {
	m, _ := peelDeckModel(t)
	m = cardCursorOn(t, m, "Llanowar Elves")

	m = key(m, "b")

	wantCursor(t, m, "Sol Ring", "main")
}

// With the board emptied its header goes too, so holding the row would land
// back on the card that just left. The cursor steps up instead.
func TestBStepsUpWhenItEmptiesABoard(t *testing.T) {
	m, _ := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Pyroblast")

	m = key(m, "b")

	wantCursor(t, m, "Lightning Bolt", "main")
}

// maybe cycles back to main, which moves the row up the list rather than down.
// The rule is about the board being peeled, not the direction of travel.
func TestBHoldsTheBoardWhenTheCardMovesUpTheList(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Lightning Bolt", "main", finish.Nonfoil, 1, 5),
		entry("Brainstorm", "maybe", finish.Nonfoil, 1, 30),
		entry("Ponder", "maybe", finish.Nonfoil, 1, 20),
	}
	m, _ := deckModelFor(t, f)
	m = cardCursorOn(t, m, "Brainstorm")

	m = key(m, "b")

	wantCursor(t, m, "Ponder", "maybe")
}

// A stack still being peeled keeps the cursor where it is; that is the
// behaviour this change must not disturb.
func TestBStillHoldsAStackWithCopiesLeft(t *testing.T) {
	m, _ := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Lightning Bolt")

	m = key(m, "b")

	c := cursorCard(t, m)
	if c.Name != "Lightning Bolt" || boardKey(c.Board) != "main" || c.Quantity != 2 {
		t.Errorf("cursor is on %s (%s board) ×%d, want Lightning Bolt (main board) ×2",
			c.Name, boardKey(c.Board), c.Quantity)
	}
}

// threeBoardDeckModel has two cards on each of the main and side boards and one
// on the maybeboard, so a jump lands on a board's first card rather than its
// only card.
func threeBoardDeckModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
		entry("Sol Ring", "main", finish.Nonfoil, 1, 50),
		entry("Pyroblast", "side", finish.Nonfoil, 1, 75),
		entry("Duress", "side", finish.Nonfoil, 1, 25),
		entry("Ponder", "maybe", finish.Nonfoil, 1, 30),
	}
	return deckModelFor(t, f)
}

func TestBracketJumpsToTheNextBoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "]")

	wantCursor(t, m, "Pyroblast", "side")
}

func TestBracketJumpsOnThroughEveryBoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "]")
	m = key(m, "]")

	wantCursor(t, m, "Ponder", "maybe")
}

func TestBracketJumpsBackToThePreviousBoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Ponder")

	m = key(m, "[")

	wantCursor(t, m, "Pyroblast", "side")
}

// A jump lands on the board's first card even when the cursor started part way
// down the board it is leaving.
func TestBracketJumpsFromTheMiddleOfABoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Duress")

	m = key(m, "[")

	wantCursor(t, m, "Force of Will", "main")
}

func TestBracketStaysPutAtTheLastBoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Ponder")

	m = key(m, "]")

	wantCursor(t, m, "Ponder", "maybe")
}

func TestBracketStaysPutAtTheFirstBoard(t *testing.T) {
	m, _ := threeBoardDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "[")

	wantCursor(t, m, "Force of Will", "main")
}

func TestBracketDoesNothingInADeckOnOneBoard(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
		entry("Sol Ring", "main", finish.Nonfoil, 1, 50),
	}
	m, _ := deckModelFor(t, f)
	m = cardCursorOn(t, m, "Sol Ring")

	m = key(m, "]")

	wantCursor(t, m, "Sol Ring", "main")
}

// The market and dip views offer ]/[ in their help line; a deck's boards are
// tables in the same sense, so they say so too.
func TestTheDeckHelpLineOffersTheBoardJumpKeys(t *testing.T) {
	m, _ := threeBoardDeckModel(t)

	if help := m.helpLine(); !strings.Contains(help, "]/[") {
		t.Errorf("deck help = %q, want the board jump keys offered", help)
	}
}

// One board is no board sections, which is why the headers are hidden there
// too. Nothing to jump between, nothing to advertise.
func TestASingleBoardDeckDoesNotOfferTheBoardJumpKeys(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
		entry("Sol Ring", "main", finish.Nonfoil, 1, 50),
	}
	m, _ := deckModelFor(t, f)

	if help := m.helpLine(); strings.Contains(help, "]/[") {
		t.Errorf("single-board deck help = %q, want no board jump keys", help)
	}
}
