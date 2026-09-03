package browse

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const noCardRow = -1

var boardSections = []struct{ key, label, word string }{
	{store.BoardMain, "MAINBOARD", "main deck"},
	{store.BoardCommander, "COMMANDER", "commander"},
	{store.BoardSide, "SIDEBOARD", "sideboard"},
	{store.BoardMaybe, "MAYBEBOARD", "maybeboard"},
}

func boardKey(board string) string {
	if board == "" {
		return store.BoardMain
	}
	return board
}

func boardRank(board string) int {
	key := boardKey(board)
	for i, s := range boardSections {
		if s.key == key {
			return i
		}
	}
	return len(boardSections)
}

func boardLabel(board string) string {
	key := boardKey(board)
	for _, s := range boardSections {
		if s.key == key {
			return s.label
		}
	}
	return strings.ToUpper(key)
}

func boardWord(board string) string {
	key := boardKey(board)
	for _, s := range boardSections {
		if s.key == key {
			return s.word
		}
	}
	return key + " board"
}

func boardCompare(a, b string) int {
	ra, rb := boardRank(a), boardRank(b)
	if ra != rb {
		return cmp.Compare(ra, rb)
	}
	if ra == len(boardSections) {
		return strings.Compare(boardKey(a), boardKey(b))
	}
	return 0
}

func nextBoard(board string) string {
	switch boardKey(board) {
	case store.BoardMain:
		return store.BoardSide
	case store.BoardSide:
		return store.BoardMaybe
	}
	return store.BoardMain
}

func parseBoard(text string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "", "main", "mainboard", "deck", "md":
		return store.BoardMain, nil
	case "commander", "cmd", "command", "general":
		return store.BoardCommander, nil
	case "side", "sideboard", "sb":
		return store.BoardSide, nil
	case "maybe", "maybeboard", "mb":
		return store.BoardMaybe, nil
	}
	return "", fmt.Errorf("a board is main, commander, side, or maybe")
}

func (m Model) inDeck() bool {
	sel := m.selectedContainer()
	return sel != nil && sel.Kind == store.KindDeck
}

func (m Model) boardSplit() bool {
	if m.view != viewHoldings || !m.inDeck() {
		return false
	}
	for i := 1; i < len(m.cards); i++ {
		if boardKey(m.cards[i-1].Board) != boardKey(m.cards[i].Board) {
			return true
		}
	}
	return false
}

func (m Model) boardHeads() []int {
	if !m.boardSplit() {
		return nil
	}
	var heads []int
	for i, c := range m.cards {
		if i == 0 || boardKey(m.cards[i-1].Board) != boardKey(c.Board) {
			heads = append(heads, i)
		}
	}
	return heads
}

func boardHeadRows(group int) int {
	if group == 0 {
		return 1
	}
	return 2
}

func cardRowAt(heads []int, i int) int {
	row := i
	for k, head := range heads {
		if head > i {
			break
		}
		row += boardHeadRows(k)
	}
	return row
}

func rowCardAt(heads []int, row int) int {
	used := 0
	for k, head := range heads {
		if row < head+used {
			break
		}
		used += boardHeadRows(k)
		if row < head+used {
			return noCardRow
		}
	}
	return row - used
}

func (m Model) cardRow(i int) int { return cardRowAt(m.boardHeads(), i) }

func (m Model) boardCopies(from int) int {
	copies := 0
	for i := from; i < len(m.cards); i++ {
		if boardKey(m.cards[i].Board) != boardKey(m.cards[from].Board) {
			break
		}
		copies += m.cards[i].Quantity
	}
	return copies
}

func boardHeaderText(board string, copies int) string {
	return fmt.Sprintf("%s (%s)", boardLabel(board), ui.Count(copies))
}

func (m Model) boardHeaderWidth() int {
	wide := 0
	for _, head := range m.boardHeads() {
		wide = max(wide, ui.Width(boardHeaderText(m.cards[head].Board, m.boardCopies(head))))
	}
	return wide
}

func boardHeaderCells(cols []ui.Col, board string, copies int) []ui.Cell {
	cells := make([]ui.Cell, len(cols))
	for i, c := range cols {
		if c.Title == "NAME" {
			cells[i] = ui.C(boardHeaderText(board, copies))
			continue
		}
		cells[i] = ui.C("")
	}
	return cells
}

func (m Model) headedRow(row int) bool {
	if row <= 0 {
		return false
	}
	heads := m.boardHeads()
	return rowCardAt(heads, row) != noCardRow && rowCardAt(heads, row-1) == noCardRow
}

// boardPlace reports where the cursor stands on a board: the row its section
// starts at, and how far down the section the cursor has travelled.
func (m Model) boardPlace(board string) (start, within int) {
	at := m.cardsPage*singleTablePageSize + m.cursor[paneCards]
	start = -1
	for i, c := range m.filteredCards {
		if boardKey(c.Board) != board {
			continue
		}
		if start < 0 {
			start = i
		}
		if i < at {
			within++
		}
	}
	if start < 0 {
		start = 0
	}
	return start, within
}

// boardTarget is the row the cursor takes once a card has left the board: the
// one that fell into its place, or the board's new last row when it was the
// last, or the row above the section when the board is gone entirely.
func (m Model) boardTarget(board string, start, within int) int {
	target, seen := -1, 0
	for i, c := range m.filteredCards {
		if boardKey(c.Board) != board {
			continue
		}
		if seen <= within {
			target = i
		}
		seen++
	}
	if target < 0 {
		return max(start-1, 0)
	}
	return target
}

// jumpBoardSection moves to the first row of the neighbouring board. The card
// list is ordered by board, so the next section is the first row outranking
// this one; it stops at either end rather than wrapping.
func (m *Model) jumpBoardSection(dir int) {
	cur := m.selectedCard()
	if cur == nil {
		return
	}
	from := boardRank(cur.Board)

	if dir > 0 {
		for i, c := range m.filteredCards {
			if boardRank(c.Board) > from {
				m.focus = paneCards
				m.focusCardAt(i)
				return
			}
		}
		return
	}

	prev, target := -1, -1
	for i, c := range m.filteredCards {
		r := boardRank(c.Board)
		if r >= from {
			break
		}
		if r != prev {
			prev, target = r, i
		}
	}
	if target >= 0 {
		m.focus = paneCards
		m.focusCardAt(target)
	}
}
