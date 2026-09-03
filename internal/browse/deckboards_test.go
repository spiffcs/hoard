package browse

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func (f *fakeStore) MoveEntryBoard(from store.EntryRef, toBoard string, copies int) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if from.Board == toBoard || copies <= 0 {
		return 0, nil
	}
	entries := f.deckCards[from.ContainerID]
	on := func(e store.EntryView, board string) bool {
		return e.Card.ScryfallID == from.ScryfallID && e.Finish == from.Finish &&
			e.Board == board
	}

	var held, prevTarget int
	for _, e := range entries {
		switch {
		case on(e, from.Board):
			held = e.Quantity
		case on(e, toBoard):
			prevTarget = e.Quantity
		}
	}
	if held == 0 {
		return 0, errFake{}
	}
	moved := min(copies, held)

	landed := false
	kept := entries[:0:0]
	for _, e := range entries {
		switch {
		case on(e, from.Board):
			if e.Quantity -= moved; e.Quantity <= 0 {
				continue
			}
		case on(e, toBoard):
			e.Quantity += moved
			landed = true
		}
		kept = append(kept, e)
	}
	if !landed {
		e := store.EntryView{Finish: from.Finish, Condition: from.Condition,
			Board: toBoard, Quantity: moved}
		for _, cand := range entries {
			if cand.Card.ScryfallID == from.ScryfallID {
				e.Card = cand.Card
				break
			}
		}
		kept = append(kept, e)
	}
	f.deckCards[from.ContainerID] = kept

	if moved == held {
		for name, holdings := range f.holdingsByName {
			for i := range holdings {
				h := &holdings[i]
				if h.ContainerID == from.ContainerID && h.ScryfallID == from.ScryfallID &&
					h.Board == from.Board {
					h.Board = toBoard
				}
			}
			f.holdingsByName[name] = holdings
		}
	}
	return prevTarget, nil
}

// boardedDeckModel puts a deck on screen whose boards interleave when the rows
// are sorted by value: the two most valuable cards sit on different boards.
func boardedDeckModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Llanowar Elves", "main", finish.Nonfoil, 1, 1),
		entry("Sol Ring", "side", finish.Nonfoil, 2, 2),
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
		entry("Pyroblast", "side", finish.Nonfoil, 1, 50),
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

func lineWith(t *testing.T, view, want string) int {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(line, want) {
			return i
		}
	}
	t.Fatalf("no line holding %q in frame:\n%s", want, view)
	return -1
}

func TestADeckListsItsSideboardBelowItsMainboard(t *testing.T) {
	m, _ := boardedDeckModel(t)

	view := m.View()
	mainHead := lineWith(t, view, "MAINBOARD")
	sideHead := lineWith(t, view, "SIDEBOARD")

	for _, name := range []string{"Force of Will", "Llanowar Elves"} {
		if at := lineWith(t, view, name); at < mainHead || at > sideHead {
			t.Errorf("%s is on line %d, want it under MAINBOARD (%d) and above SIDEBOARD (%d)",
				name, at, mainHead, sideHead)
		}
	}
	for _, name := range []string{"Pyroblast", "Sol Ring"} {
		if at := lineWith(t, view, name); at < sideHead {
			t.Errorf("%s is on line %d, above the SIDEBOARD header on %d", name, at, sideHead)
		}
	}
}

func TestABoardHeaderCountsTheCopiesBesideItsName(t *testing.T) {
	m, _ := boardedDeckModel(t)

	lines := strings.Split(m.View(), "\n")
	main := lines[lineWith(t, m.View(), "MAINBOARD")]
	side := lines[lineWith(t, m.View(), "SIDEBOARD")]

	if !strings.Contains(main, "MAINBOARD (2)") {
		t.Errorf("mainboard header = %q, want MAINBOARD (2)", main)
	}
	if !strings.Contains(side, "SIDEBOARD (3)") {
		t.Errorf("sideboard header = %q, want SIDEBOARD (3)", side)
	}
	if strings.Contains(main, "×2") || strings.Contains(side, "×3") {
		t.Errorf("a board header still fills the QTY column:\n%s\n%s", main, side)
	}
}

func TestABoardHeaderSurvivesADeckOfShortNames(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Bolt", "main", finish.Nonfoil, 4, 1),
		entry("Duress", "side", finish.Nonfoil, 2, 1),
		entry("Ponder", "maybe", finish.Nonfoil, 1, 1),
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}

	lines := m.cardLines(60)
	joined := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"MAINBOARD (4)", "SIDEBOARD (2)", "MAYBEBOARD (1)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in a deck of short card names:\n%s", want, joined)
		}
	}
}

func TestBoardHeadersAreNotRowsTheCursorCanLandOn(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	m, _ := boardedDeckModel(t)

	for i := range m.cards {
		want := m.cards[i].Name
		if got := m.selectedCard().Name; got != want {
			t.Fatalf("cursor %d selects %q, want %q", i, got, want)
		}
		var barred string
		for line := range strings.SplitSeq(m.View(), "\n") {
			if strings.Contains(line, "\x1b[7m") {
				barred = line
				break
			}
		}
		if barred == "" {
			t.Fatalf("nothing is highlighted with the cursor on %q:\n%s", want, m.View())
		}
		if !strings.Contains(barred, want) {
			t.Errorf("the cursor bar sits on %q, want it on %q", strings.TrimSpace(barred), want)
		}
		m = key(m, "down")
	}
}

func TestADeckOnOneBoardShowsNoBoardHeaders(t *testing.T) {
	m, _ := deckEditModel(t)
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Llanowar Elves", "main", finish.Nonfoil, 1, 1),
		entry("Force of Will", "main", finish.Nonfoil, 1, 100),
	}
	m.store = f
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}

	if view := m.View(); strings.Contains(view, "MAINBOARD") {
		t.Errorf("a deck with nothing but a main deck announced its board:\n%s", view)
	}
}

func TestBoardSectionsRunInDecklistOrder(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Sol Ring", "side", finish.Nonfoil, 1, 2),
		entry("Ulamog", "commander", finish.Nonfoil, 1, 30),
		entry("Brainstorm", "maybe", finish.Nonfoil, 1, 1),
		entry("Llanowar Elves", "main", finish.Nonfoil, 1, 1),
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}

	view := m.View()
	order := []string{"MAINBOARD", "COMMANDER", "SIDEBOARD", "MAYBEBOARD"}
	at := make([]int, len(order))
	for i, head := range order {
		at[i] = lineWith(t, view, head)
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("%s (line %d) renders above %s (line %d)",
				order[i], at[i], order[i-1], at[i-1])
		}
	}
}

func deckDetailModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	m, f := boardedDeckModel(t)
	f.holdingsByName = map[string][]store.Holding{}
	f.holdingsByName["Sol Ring"] = []store.Holding{{
		ContainerID: 201, ContainerName: "Cheap Deck", ContainerKind: store.KindDeck,
		ScryfallID: "Sol Ring-id", Finish: finish.Nonfoil,
		Condition: store.ConditionUnknown, Board: "side", Quantity: 2,
	}}
	m = cardCursorOn(t, m, "Sol Ring")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter on a deck row opened no card detail")
	}
	m.detail.zone, m.detail.heldField = zoneHeld, fieldWhere
	return m, f
}

func TestTheDetailViewMovesADeckCardBetweenBoards(t *testing.T) {
	m, f := deckDetailModel(t)

	m.editHeldField()
	if m.prompt == nil {
		t.Fatalf("no prompt for a deck card's board: status = %q", m.status)
	}
	if !strings.Contains(m.prompt.label, "board") {
		t.Errorf("prompt label = %q, want it to say it edits the board", m.prompt.label)
	}
	if m.prompt.text != "side" {
		t.Errorf("prompt prefilled with %q, want the board the card is on", m.prompt.text)
	}

	m.prompt.commit(&m, "main")
	m.prompt = nil

	if m.statusErr {
		t.Fatalf("moving a deck card between boards was refused: %q", m.status)
	}
	if got := deckQuantity(t, f, 201, "Sol Ring", "main"); got != 2 {
		t.Errorf("main deck holds %d Sol Ring, want the 2 moved over", got)
	}
	for _, e := range f.deckCards[201] {
		if e.Card.Name == "Sol Ring" && e.Board == "side" {
			t.Errorf("Sol Ring is still on the sideboard ×%d", e.Quantity)
		}
	}
}

func TestAnUnknownBoardIsRefused(t *testing.T) {
	m, _ := deckDetailModel(t)

	m.editHeldField()
	if m.prompt == nil {
		t.Fatalf("no prompt for a deck card's board: status = %q", m.status)
	}
	if err := m.prompt.validate("bogus"); err == nil {
		t.Error("validate accepted a board that is not a board")
	}
	if err := m.prompt.validate("sideboard"); err != nil {
		t.Errorf("validate rejected the long spelling of a board: %v", err)
	}
}

func TestABinderCardStillMovesBetweenBinders(t *testing.T) {
	m, f := boardedDeckModel(t)
	f.holdingsByName = map[string][]store.Holding{}
	f.holdingsByName["Sol Ring"] = []store.Holding{{
		ContainerID: defaultBinderID, ContainerName: "Binder",
		ContainerKind: store.KindCollection, ScryfallID: "Sol Ring-id",
		Finish: finish.Nonfoil, Condition: store.ConditionUnknown,
		Board: "main", Quantity: 3,
	}}
	m.detail = &detail{holdings: f.holdingsByName["Sol Ring"]}

	m.promptHeldLocation()

	if m.prompt == nil {
		t.Fatalf("no prompt for a binder card's location: status = %q", m.status)
	}
	if !strings.Contains(m.prompt.label, "binder") {
		t.Errorf("prompt label = %q, want it to still ask for a binder", m.prompt.label)
	}
}

func TestReversingTheSortKeepsTheBoardsInOrder(t *testing.T) {
	m, _ := boardedDeckModel(t)

	m = key(m, "S")

	if m.sortLabel() != "value (reversed)" {
		t.Fatalf("sort label = %q, want the value sort reversed", m.sortLabel())
	}
	if got := names(m.cards); !slices.Equal(got,
		[]string{"Llanowar Elves", "Force of Will", "Sol Ring", "Pyroblast"}) {
		t.Errorf("reversed = %v, want each board reversed but the main deck still first", got)
	}
}

func TestALongDeckScrollsWithoutLosingTheCursor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	f := testStore()
	f.deckCards[201] = nil
	for i := range 30 {
		f.deckCards[201] = append(f.deckCards[201],
			entry("Main "+strconv.Itoa(i), "main", finish.Nonfoil, 1, float64(i+1)))
	}
	for i := range 10 {
		f.deckCards[201] = append(f.deckCards[201],
			entry("Side "+strconv.Itoa(i), "side", finish.Nonfoil, 1, float64(i+1)))
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.focus = paneCards

	for range len(m.cards) + 3 {
		want := m.selectedCard().Name
		view := m.View()
		if n := len(strings.Split(view, "\n")); n > 14 {
			t.Fatalf("rendered %d lines at height 14", n)
		}
		var barred string
		for line := range strings.SplitSeq(view, "\n") {
			if strings.Contains(line, "\x1b[7m") {
				barred = line
				break
			}
		}
		if barred == "" {
			t.Fatalf("the cursor scrolled off the frame at %q:\n%s", want, view)
		}
		if !strings.Contains(barred, want) {
			t.Fatalf("the cursor bar sits on %q, want it on %q", strings.TrimSpace(barred), want)
		}
		m = key(m, "down")
	}
}

func TestABlankLineSetsEachBoardApart(t *testing.T) {
	m, _ := boardedDeckModel(t)

	lines := m.cardLines(80)
	blank := func(i int) bool {
		return i >= 0 && i < len(lines) && strings.TrimSpace(ansi.Strip(lines[i])) == ""
	}
	at := func(want string) int {
		for i, line := range lines {
			if strings.Contains(ansi.Strip(line), want) {
				return i
			}
		}
		t.Fatalf("no %q in the card pane:\n%s", want, strings.Join(lines, "\n"))
		return -1
	}

	if main := at("MAINBOARD"); blank(main - 1) {
		t.Errorf("a blank line sits above the first board header:\n%s", strings.Join(lines, "\n"))
	}
	if side := at("SIDEBOARD"); !blank(side - 1) {
		t.Errorf("no blank line above SIDEBOARD:\n%s", strings.Join(lines, "\n"))
	}
}

func boardOf(t *testing.T, f *fakeStore, deckID int64, name string) string {
	t.Helper()
	for _, e := range f.deckCards[deckID] {
		if e.Card.Name == name {
			return e.Board
		}
	}
	t.Fatalf("no %q in deck %d", name, deckID)
	return ""
}

func TestBMovesTheCardUnderTheCursorToTheNextBoard(t *testing.T) {
	m, f := boardedDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "b")

	if m.statusErr {
		t.Fatalf("b was refused: %q", m.status)
	}
	if got := boardOf(t, f, 201, "Force of Will"); got != "side" {
		t.Errorf("Force of Will is on the %s board, want side", got)
	}
	c := m.selectedCard()
	if c == nil {
		t.Fatal("no card under the cursor")
	}
	if c.Name == "Force of Will" {
		t.Errorf("the cursor chased the card onto the %s board", boardKey(c.Board))
	}
	if got := boardKey(c.Board); got != "main" {
		t.Errorf("the cursor left the main deck for the %s board", got)
	}
}

func TestBCyclesThroughTheBoardsAndBackToTheMainDeck(t *testing.T) {
	m, f := boardedDeckModel(t)

	// The cursor no longer chases the card, so following one all the way round
	// means putting the cursor back on it before each press.
	for _, want := range []string{"side", "maybe", "main"} {
		m = cardCursorOn(t, m, "Force of Will")
		m = key(m, "b")
		if m.statusErr {
			t.Fatalf("b was refused on the way to %s: %q", want, m.status)
		}
		if got := boardOf(t, f, 201, "Force of Will"); got != want {
			t.Fatalf("Force of Will landed on %s, want %s", got, want)
		}
	}
}

func TestBLeavesACommanderWhereItIs(t *testing.T) {
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Ulamog", "commander", finish.Nonfoil, 1, 30),
		entry("Sol Ring", "main", finish.Nonfoil, 1, 2),
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.focus = paneCards
	m = cardCursorOn(t, m, "Ulamog")

	m = key(m, "b")

	if !m.statusErr {
		t.Errorf("b moved a commander off its board: status = %q", m.status)
	}
	if got := boardOf(t, f, 201, "Ulamog"); got != "commander" {
		t.Errorf("Ulamog is on the %s board, want commander", got)
	}
}

func TestUndoPutsACycledCardBackOnItsBoard(t *testing.T) {
	m, f := boardedDeckModel(t)
	m = cardCursorOn(t, m, "Force of Will")

	m = key(m, "b")
	m = key(m, "u")

	if m.statusErr {
		t.Fatalf("undo was refused: %q", m.status)
	}
	if got := boardOf(t, f, 201, "Force of Will"); got != "main" {
		t.Errorf("Force of Will stayed on the %s board, want the main deck back", got)
	}
	if got := deckQuantity(t, f, 201, "Force of Will", "main"); got != 1 {
		t.Errorf("the main deck holds %d Force of Will, want the 1 it started with", got)
	}
}

func TestTheDeckHelpLineOffersTheBoardKey(t *testing.T) {
	m, _ := boardedDeckModel(t)
	if help := m.helpLine(); !strings.Contains(help, "board") {
		t.Errorf("deck help = %q, want the board key offered", help)
	}

	m = atAllCards(t, m)
	m.focus = paneCards
	if help := m.helpLine(); strings.Contains(help, "board") {
		t.Errorf("all-cards help = %q, want no board key outside a deck", help)
	}
}

func stackedDeckModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Lightning Bolt", "main", finish.Nonfoil, 3, 5),
		entry("Pyroblast", "side", finish.Nonfoil, 1, 50),
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

func TestBMovesOneCopyAtATime(t *testing.T) {
	m, f := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Lightning Bolt")

	m = key(m, "b")

	if m.statusErr {
		t.Fatalf("b was refused: %q", m.status)
	}
	if got := deckQuantity(t, f, 201, "Lightning Bolt", "main"); got != 2 {
		t.Errorf("the main deck holds %d Lightning Bolt, want the 2 left after one moved", got)
	}
	if got := deckQuantity(t, f, 201, "Lightning Bolt", "side"); got != 1 {
		t.Errorf("the sideboard holds %d Lightning Bolt, want the 1 that moved", got)
	}
}

func TestBKeepsTheCursorOnAStackItIsStillPeeling(t *testing.T) {
	m, f := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Lightning Bolt")

	m = key(m, "b")

	c := m.selectedCard()
	if c == nil || c.Name != "Lightning Bolt" || c.Board != "main" {
		t.Fatalf("the cursor left the stack it was peeling: %+v", c)
	}
	if c.Quantity != 2 {
		t.Errorf("the cursor sits on a row of %d, want the 2 still on the main deck", c.Quantity)
	}

	m = key(m, "b")
	m = key(m, "b")

	if got := deckQuantity(t, f, 201, "Lightning Bolt", "side"); got != 3 {
		t.Errorf("three presses moved %d copies to the sideboard, want all 3", got)
	}
	for _, e := range f.deckCards[201] {
		if e.Card.Name == "Lightning Bolt" && e.Board == "main" {
			t.Errorf("the main deck still holds %d Lightning Bolt", e.Quantity)
		}
	}
}

func TestBLeavesTheCursorOnTheBoardItPeeledFrom(t *testing.T) {
	m, f := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Pyroblast")

	m = key(m, "b")

	if got := boardOf(t, f, 201, "Pyroblast"); got != "maybe" {
		t.Fatalf("Pyroblast is on the %s board, want maybe", got)
	}
	c := m.selectedCard()
	if c == nil {
		t.Fatal("no card under the cursor")
	}
	if c.Name == "Pyroblast" {
		t.Errorf("the cursor chased the copy onto the %s board", boardKey(c.Board))
	}
}

func TestUndoRestoresAPeeledCopy(t *testing.T) {
	m, f := stackedDeckModel(t)
	m = cardCursorOn(t, m, "Lightning Bolt")

	m = key(m, "b")
	m = key(m, "u")

	if m.statusErr {
		t.Fatalf("undo was refused: %q", m.status)
	}
	if got := deckQuantity(t, f, 201, "Lightning Bolt", "main"); got != 3 {
		t.Errorf("the main deck holds %d Lightning Bolt, want the 3 it started with", got)
	}
	for _, e := range f.deckCards[201] {
		if e.Card.Name == "Lightning Bolt" && e.Board == "side" {
			t.Errorf("a peeled copy stayed on the sideboard ×%d", e.Quantity)
		}
	}
}
