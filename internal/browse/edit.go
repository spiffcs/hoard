package browse

import (
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
)

// undoAction is how to put back what the last edit changed.
//
// One action, not a stack. Undo exists for the keystroke you did not mean —
// leaning on `-`, hitting `d` on the wrong row — and one level covers that. A
// stack invites the question of how far back it goes and, worse, tempts you to
// treat a keystroke-driven browser as a place where changes are provisional.
// They are not: every edit is committed before the frame is drawn.
type undoAction struct {
	// desc is what will be undone, phrased for the status line.
	desc string
	// undo performs the restoration. Returning an error leaves the action in
	// place so it can be tried again.
	undo func(Editor) error
}

// editable reports whether the selected container's cards can be changed here,
// explaining itself when they cannot.
//
// Deck entries are deliberately read-only. A deck is owned by the list it was
// imported from, and editing it here would silently diverge from that source
// until the next `deck add` overwrote the change without warning.
func (m Model) editable() (bool, string) {
	// The analytical views list different rows than the holdings pane, so the
	// cursor indexes a different slice. Editing here would silently change
	// whichever holding happened to sit at the same offset — a card the reader
	// is not looking at and did not name.
	if m.view != viewHoldings {
		return false, "editing works on holdings — press v to come back"
	}
	sel := m.selectedContainer()
	if sel == nil {
		return false, ""
	}
	if sel.Kind != store.KindCollection {
		return false, "deck cards are owned by the imported list — edit the " +
			strings.ToLower(store.LooseName) + " instead"
	}
	return true, ""
}

// adjustQuantity changes the selected holding by delta.
func (m *Model) adjustQuantity(delta int) {
	ok, why := m.editable()
	if !ok {
		if why != "" {
			m.status, m.statusErr = why, true
		}
		return
	}
	c := m.selectedCard()
	if c == nil {
		return
	}

	want := max(c.Quantity+delta, 0)
	if want == c.Quantity {
		return
	}
	previous, err := m.store.SetHoldingQuantity(c.ScryfallID, c.Finish, want)
	if err != nil {
		m.setError(err)
		return
	}

	id, finish, name := c.ScryfallID, c.Finish, c.Name
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetHoldingQuantity(id, finish, previous)
			return err
		},
	})
	if want == 0 {
		m.status = fmt.Sprintf("removed %s (%s)", name, finish)
	} else {
		m.status = fmt.Sprintf("%s (%s) ×%d", name, finish, want)
	}
	m.statusErr = false
	m.refresh()
}

// removeCard drops every finish of the selected printing from the collection.
func (m *Model) removeCard() {
	ok, why := m.editable()
	if !ok {
		if why != "" {
			m.status, m.statusErr = why, true
		}
		return
	}
	c := m.selectedCard()
	if c == nil {
		return
	}

	removed, err := m.store.RemoveFromCollection(c.ScryfallID)
	if err != nil {
		m.setError(err)
		return
	}
	id, name := c.ScryfallID, c.Name
	m.undoable(undoAction{
		desc: name,
		undo: func(st Editor) error { return st.RestoreHoldings(id, removed) },
	})
	m.status = fmt.Sprintf("removed %s from the %s", name, strings.ToLower(store.LooseName))
	m.statusErr = false
	m.refresh()
}

// removeDeck deletes the selected deck.
func (m *Model) removeDeck() {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind == store.KindCollection {
		m.status, m.statusErr = "the "+strings.ToLower(store.LooseName)+" cannot be removed", true
		return
	}

	// Read the entries before the delete, not after: RemoveContainer cascades
	// to card_entries, so afterwards there is nothing left to reconstruct from.
	views, err := m.store.DeckEntries(sel.ID)
	if err != nil {
		m.setError(err)
		return
	}
	entries := make([]store.Entry, 0, len(views))
	for _, v := range views {
		entries = append(entries, store.Entry{
			ScryfallID: v.Card.ScryfallID, Finish: v.Finish,
			Board: v.Board, Quantity: v.Quantity,
		})
	}
	meta, name := sel.meta, sel.Name

	if _, err := m.store.RemoveContainer(sel.ID); err != nil {
		m.setError(err)
		return
	}
	m.undoable(undoAction{
		desc: name,
		undo: func(st Editor) error {
			// Recreated rather than resurrected: the deck comes back with a new
			// container id. Its identity to the rest of hoard is (source,
			// source_id), which the metadata carries, so a later re-import still
			// updates this deck rather than adding a second copy of it.
			_, err := st.UpsertDeck(meta, entries)
			return err
		},
	})
	m.status = fmt.Sprintf("removed deck %s", name)
	m.statusErr = false
	m.refresh()
}

// undoRecorded restores whatever the last edit changed.
func (m *Model) undoRecorded() {
	if m.undoStack == nil {
		m.status, m.statusErr = "nothing to undo", false
		return
	}
	action := *m.undoStack
	if err := action.undo(m.store); err != nil {
		m.setError(err)
		return
	}
	m.undoStack = nil
	m.status = "restored " + action.desc
	m.statusErr = false
	m.refresh()
}

// undoable records how to reverse the edit that just happened, replacing any
// previous one.
func (m *Model) undoable(a undoAction) { m.undoStack = &a }

// refresh re-reads both panes after an edit, keeping the cursor where it was.
//
// Totals live on the container rows, so an edit to one card changes the left
// pane as well as the right; re-reading only the cards would leave the
// collection's value stale on screen while the row under the cursor showed the
// new number.
func (m *Model) refresh() {
	cards, containers := m.cursor[paneCards], m.cursor[paneContainers]
	if err := m.loadContainers(); err != nil {
		m.setError(err)
		return
	}
	m.cursor[paneContainers] = containers
	m.clampCursor(paneContainers)
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	m.cursor[paneCards] = cards
	m.clampCursor(paneCards)
	m.scrollIntoView()
}
