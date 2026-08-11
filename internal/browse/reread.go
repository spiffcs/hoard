package browse

// One re-read, one shape.
//
// Browse used to have two. refresh() served the edit paths and put the
// reader back on the row and the page they were on; reload() served the r
// key and put them back on row one of page one — while the palette
// advertised that key as "keeping your place". The disagreement was the
// defect, not either path's behavior: a re-read is a re-read, and which
// function happened to be wired to the key decided what it cost you.
//
// Both now call reread. The place is captured before the reads and put back
// after them, so growing the set of things a re-read preserves is one edit
// in one place rather than two that can drift apart again.

// place is where the reader is, and it is the definition of what a re-read
// must not spend: both panes' cursors and scroll offsets, and which page of
// the holdings the cursor is on.
//
// Deliberately not here: the filter text, the value floor, focus, the
// analytical views' per-section offsets and the movers page. Those live on
// the model and no loader touches them, so carrying them would be
// ceremony — the restore would be writing back values that never moved.
// The tests assert they survive anyway, which is the guard that keeps that
// claim true.
type place struct {
	cards, containers      int
	cardOffset, contOffset int
	cardsPage              int
	// key identifies the holdings row the card cursor was on — see
	// cardKey. Empty when the pane is empty, and when an analytical view
	// owns the cursor, because then the index is not a holdings index at
	// all. Only rereadLive uses it.
	key string
}

// rereadScope says how much of the screen a re-read touches and, because the
// two always pair, how the cursor comes back.
type rereadScope int

const (
	// rereadAll re-reads both panes and the analytical view, and puts the
	// cursor back on the index it held. That is right for an edit and for
	// the r key: the reader changed the row that is there, or asked for
	// everything, and either way they know something happened.
	rereadAll rereadScope = iota
	// rereadLive re-reads the panes only, and puts the cursor back on the
	// row it was on rather than the index it was at. The exclusions are
	// deliberate — movers is the longest lock hold in the program and
	// market needs the network — and so is the identity restore:
	// holdings sort by value descending, so an
	// insert shifts every row beneath it and an index would land the
	// cursor on a different card without saying so.
	rereadLive
)

// cardKey identifies a holdings row across a re-read.
//
// Two shapes, because the pane is not always showing the same kind of row. A
// container's own list is holdings, distinct since schema v23 by printing,
// finish and condition — plus the board, which is what makes a deck's main
// and sideboard copies two rows rather than one.
//
// The merged All Cards list is not that. loadCards folds same-name printings
// together with mergeByName, and the folded row's ScryfallID is only
// whichever constituent sorted highest by value — so a quantity change on
// any printing can silently re-elect it, and keying on it would lose the row
// precisely when something changed. There, the name and the finish are the
// row.
func (m Model) cardKey(c card) string {
	if sel := m.selectedContainer(); sel != nil && sel.Kind == kindAllCards {
		return c.Name + "|" + c.Finish
	}
	return c.ScryfallID + "|" + c.Finish + "|" + c.Condition + "|" + c.Board
}

// capturePlace reads the reader's position off the model.
func (m *Model) capturePlace() place {
	p := place{
		cards: m.cursor[paneCards], containers: m.cursor[paneContainers],
		cardOffset: m.offset[paneCards], contOffset: m.offset[paneContainers],
		cardsPage: m.cardsPage,
	}
	if m.view == viewHoldings {
		if c := m.selectedCard(); c != nil {
			p.key = m.cardKey(*c)
		}
	}
	return p
}

// seekCard puts the cursor on the filtered row carrying this key, turning to
// whichever page holds it, and reports whether the row was still there.
func (m *Model) seekCard(key string) bool {
	for i, c := range m.filteredCards {
		if m.cardKey(c) != key {
			continue
		}
		m.cardsPage = i / singleTablePageSize
		m.deriveCardsPage()
		m.cursor[paneCards] = i - m.cardsPage*singleTablePageSize
		return true
	}
	return false
}

// reread re-reads the panes — and, at rereadAll, the analytical view —
// putting the reader back where they were. Every cursor is clamped against
// counts the re-read may have shrunk, so a place that no longer exists
// becomes the nearest one that does rather than an index off the end.
//
// The container cursor goes back first, before loadCards: the right pane is
// read for whichever container is selected, so restoring the left pane
// after it would fill the right one from the wrong row.
func (m *Model) reread(scope rereadScope) error {
	// Whatever prompted the re-read may have moved prices or holdings;
	// caches keyed on the old world are done.
	m.dataGen++
	p := m.capturePlace()

	if err := m.loadContainers(); err != nil {
		return err
	}
	if err := m.rebuildEntryIndex(); err != nil {
		return err
	}
	m.cursor[paneContainers], m.offset[paneContainers] = p.containers, p.contOffset
	m.clampCursor(paneContainers)

	if err := m.loadCards(); err != nil {
		return err
	}
	// The page comes back like the cursor: an edit three pages in must not
	// drop the reader on page one (loadCards resets both for a *new*
	// container; this is the same one re-read). deriveCardsPage clamps
	// against a total the change may have shrunk.
	m.cardsPage = p.cardsPage
	m.deriveCardsPage()
	m.cursor[paneCards], m.offset[paneCards] = p.cards, p.cardOffset
	m.clampCursor(paneCards)

	// The clamped index above is the fallback, already in place: a row that
	// disappeared while the reader was looking at it leaves them on the
	// nearest one, which is a place kept even though the selection was not.
	// Silently landing on row one is the failure this whole function
	// exists to stop.
	m.rowGone = false
	if scope == rereadLive && p.key != "" {
		m.rowGone = !m.seekCard(p.key)
	}

	if scope == rereadAll {
		// The analytical views re-read too: they depend on the membership
		// this change may have touched, and "re-read" must not mean
		// "except the rows you are looking at". On holdings this costs one
		// deriveView and no query — loadView's switch has no holdings case.
		if err := m.loadView(); err != nil {
			return err
		}
	}
	// Last, because the clamps above can land the cursor outside the
	// window the offsets describe.
	m.scrollIntoView()
	return nil
}

// refresh re-reads after an edit, keeping the reader's place.
//
// Totals live on the container rows, so an edit to one card changes the left
// pane as well as the right; re-reading only the cards would leave the
// collection's value stale on screen while the row under the cursor showed
// the new number.
func (m *Model) refresh() {
	if err := m.reread(rereadAll); err != nil {
		m.setError(err)
	}
}

// reload is the r key: the same re-read, said out loud. It is what makes an
// edit made elsewhere — or an update-prices in another terminal — visible
// without restarting.
func (m *Model) reload() {
	if err := m.reread(rereadAll); err != nil {
		m.setError(err)
		return
	}
	m.status = "reloaded"
	m.statusErr = false
}
