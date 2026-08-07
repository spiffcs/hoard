package browse

// Sets mode: the left pane's second lens. B flips the COLLECTION listing
// (binders and decks) to SETS — one row per Magic set the hoard holds
// cards from, newest release first — and back. A selected set scopes the
// right pane and every analytical view exactly like a binder would.
//
// A set row is not a place, so its cards are edited by resolving each row
// back to the binders that actually hold it (setRowHoldings) rather than
// through the selected container, whose id is synthetic. Deck copies are
// never touched here, exactly as on the holdings pane.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

// loadSetContainers reads the left pane in sets mode: All Cards, then one
// row per set held, in the store's order (newest release first, unknown
// dates last).
func (m *Model) loadSetContainers() error {
	sets, err := m.store.SetsHeld()
	if err != nil {
		return fmt.Errorf("reading sets: %w", err)
	}
	out := make([]container, 0, len(sets)+1)
	out = append(out, container{ID: allCardsID, Name: allCardsName, Kind: kindAllCards})
	for i, s := range sets {
		out = append(out, container{
			// Synthetic ids below allCardsID: no store row backs a set,
			// but viewEligible and containerEligible key rows by id, so
			// each needs a unique one that can never collide with a real
			// (positive) container id.
			ID:   allCardsID - 1 - int64(i),
			Name: s.Name, Kind: kindSet, setCode: s.Code,
			Copies: s.Copies, Value: s.Value,
		})
		out[0].Copies += s.Copies
		out[0].Value += s.Value
	}
	m.containers = out
	m.clampCursor(paneContainers)
	return nil
}

// toggleSetsMode flips the left pane between binders/decks and sets. The
// row set changes entirely, so the selection resets to All Cards — the one
// row both listings share — and the card pane and views re-derive against
// it.
func (m *Model) toggleSetsMode() {
	m.setsMode = !m.setsMode
	m.cursor[paneContainers], m.offset[paneContainers] = 0, 0
	if err := m.loadContainers(); err != nil {
		// The failed read left the old pane standing; the flag must agree
		// with what is on screen.
		m.setsMode = !m.setsMode
		m.setError(err)
		return
	}
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	m.deriveView()
	if m.setsMode {
		m.status, m.statusErr = "browsing by set · newest first · B returns to binders and decks", false
	} else {
		m.status, m.statusErr = "browsing binders and decks", false
	}
}

// setRowHoldings resolves a set row back to the binders that hold it.
//
// A set row is every copy of one printing and finish, wherever it lives —
// SetByFinish groups by printing alone, so the ×7 on screen can be 4 loose
// and 3 in a deck. Editing needs the real rows behind that total, filtered
// to the binders: deck copies are owned by their imported lists everywhere
// else in hoard and are no more editable through this lens. refusal is the
// status-line explanation when nothing here can be edited; errored means
// the store already reported and the caller should stop.
func (m *Model) setRowHoldings(c *card) (binders []store.Holding, refusal string, errored bool) {
	holdings, err := m.store.HoldingsOf(c.ScryfallID)
	if err != nil {
		m.setError(err)
		return nil, "", true
	}
	deckCopies := false
	for _, h := range holdings {
		if h.Finish != c.Finish {
			continue
		}
		if h.ContainerKind != store.KindCollection {
			deckCopies = true
			continue
		}
		// Only the main board: SetHoldingQuantityIn reads and writes there,
		// so a row from anywhere else would report a zero previous count and
		// delete nothing.
		if h.Board != "" && h.Board != "main" {
			continue
		}
		binders = append(binders, h)
	}
	switch {
	case len(binders) > 0:
		return binders, "", false
	case deckCopies:
		return nil, "every copy of " + c.Name + " is in a deck · deck cards are owned by the imported list", false
	default:
		return nil, "no binder holds " + c.Name + " (" + c.Finish + ") · r reloads", false
	}
}

// adjustSetQuantity is +/- on a set row: it changes the one binder holding
// the printing. With several holding it there is no "the" binder to change,
// and guessing would move a count the reader cannot see — the detail
// overlay lists them per binder, so the refusal points there.
func (m *Model) adjustSetQuantity(delta int) {
	c := m.selectedCard()
	if c == nil {
		return
	}
	binders, refusal, errored := m.setRowHoldings(c)
	if errored {
		return
	}
	if refusal != "" {
		m.status, m.statusErr = refusal, true
		return
	}
	if len(binders) > 1 {
		m.status, m.statusErr = fmt.Sprintf(
			"%s is in %d binders · enter opens the card to edit each copy", c.Name, len(binders)), true
		return
	}

	h := binders[0]
	// The binder's own count, not the row's: the row total may include deck
	// copies, and adding to that would jump the binder past what it holds.
	want := max(h.Quantity+delta, 0)
	if want == h.Quantity {
		return
	}
	previous, err := m.store.SetHoldingQuantityIn(h.ContainerID, c.ScryfallID, c.Finish, h.Condition, want)
	if err != nil {
		m.setError(err)
		return
	}
	cid, id, finish, cond, name := h.ContainerID, c.ScryfallID, c.Finish, h.Condition, c.Name
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetHoldingQuantityIn(cid, id, finish, cond, previous)
			return err
		},
	})
	if want == 0 {
		m.status = fmt.Sprintf("removed %s (%s) from %s", name, finish, h.ContainerName)
	} else {
		m.status = fmt.Sprintf("%s (%s) ×%d in %s", name, finish, want, h.ContainerName)
	}
	m.statusErr = false
	m.refresh()
}

// askSetRemoval stages d on a set row. The prompt names where the copies
// actually are — the selected container is a set, which is not a place a
// card can be removed from.
func (m *Model) askSetRemoval() {
	c := m.selectedCard()
	if c == nil {
		return
	}
	binders, refusal, errored := m.setRowHoldings(c)
	if errored {
		return
	}
	if refusal != "" {
		m.status, m.statusErr = refusal, true
		return
	}
	name, id, finish := c.Name, c.ScryfallID, c.Finish
	prompt := fmt.Sprintf("remove %s (%s) from %d binders?", name, finish, len(binders))
	if len(binders) == 1 {
		prompt = fmt.Sprintf("remove %s (%s) ×%d from %s?",
			name, finish, binders[0].Quantity, binders[0].ContainerName)
	}
	m.confirm = &pendingConfirm{
		prompt: prompt,
		help:   "y remove · any other key cancels",
		onYes:  func(m *Model) tea.Cmd { m.removeSetRow(name, id, finish, binders); return nil },
	}
}

// removeSetRow drops the confirmed printing from every binder holding it,
// under one undo.
//
// Only this row's finish, unlike the binder pane's d (which drops every
// finish of the printing through RemoveFromBinder). The row the reader
// confirmed against reads "nonfoil ×4"; taking an unlisted foil copy along
// with it would remove something the prompt never named — and this removal
// already reaches across containers, so widening it across finishes too
// would compound what one keystroke does.
func (m *Model) removeSetRow(name, id, finish string, binders []store.Holding) {
	var removed []store.Holding
	record := func() {
		if len(removed) == 0 {
			return
		}
		gone := removed
		m.undoable(undoAction{
			desc: name,
			undo: func(st Editor) error { return st.RestoreHoldings(id, gone) },
		})
	}
	for _, h := range binders {
		previous, err := m.store.SetHoldingQuantityIn(h.ContainerID, id, finish, h.Condition, 0)
		if err != nil {
			// Whatever was already removed stays undoable: a half-finished
			// removal the reader cannot reverse is the worse failure.
			record()
			m.setError(err)
			return
		}
		removed = append(removed, store.Holding{
			ContainerID: h.ContainerID, ContainerName: h.ContainerName,
			ContainerKind: store.KindCollection, Finish: finish,
			Board: "main", Quantity: previous,
		})
	}
	record()
	if len(binders) == 1 {
		m.status = fmt.Sprintf("removed %s (%s) from %s", name, finish, binders[0].ContainerName)
	} else {
		m.status = fmt.Sprintf("removed %s (%s) from %d binders", name, finish, len(binders))
	}
	m.statusErr = false
	m.refresh()
}
