package browse

// Sets mode: the left pane's second lens. B flips the COLLECTION listing
// (binders and decks) to SETS — one row per Magic set the hoard holds
// cards from, newest release first — and back. A selected set scopes the
// right pane and every analytical view exactly like a binder would; the
// rows themselves are read-only, because a set is how cards were printed,
// not where they live.

import "fmt"

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
