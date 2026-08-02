package browse

// The container pane as a filter. Every view reads its rows through the
// selected container: All cards means unfiltered, anything else narrows the
// view to what that container holds. Membership is answered by an index of
// (container, printing, finish) facts rather than by names — container
// labels are not unique — and the same index says which containers are
// worth selecting at all on the views that grey the rest out.

import (
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// entryKey is the index's inner key: one printing in one finish.
func entryKey(scryfallID, finish string) string {
	return scryfallID + "|" + finish
}

// rebuildEntryIndex re-reads the membership facts. Called wherever holdings
// change: New, reload, and refresh (which covers edits, removals, and the
// add cascade's writes).
func (m *Model) rebuildEntryIndex() error {
	keys, err := m.store.EntryKeys()
	if err != nil {
		m.entryIndex = nil
		return err
	}
	idx := make(map[int64]map[string]bool)
	for _, k := range keys {
		set := idx[k.ContainerID]
		if set == nil {
			set = make(map[string]bool)
			idx[k.ContainerID] = set
		}
		set[entryKey(k.ScryfallID, k.Finish)] = true
	}
	m.entryIndex = idx
	return nil
}

// filterContainerID is the container the views must filter to, ok=false
// when the selection is All cards — no filtering.
func (m Model) filterContainerID() (int64, bool) {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind == kindAllCards {
		return 0, false
	}
	return sel.ID, true
}

// inContainer reports whether the container holds the printing in exactly
// this finish — the join for rows that carry entry finishes (unpriced,
// market, comps).
func (m Model) inContainer(cid int64, scryfallID, finish string) bool {
	return m.entryIndex[cid][entryKey(scryfallID, finish)]
}

// inContainerPriced is the join for rows keyed by price finish (movers,
// watches): price feeds fold etched into foil, so a foil-priced row matches
// a container holding the foil or the etched printing.
func (m Model) inContainerPriced(cid int64, scryfallID, priceFinish string) bool {
	if scryfall.PricedAsFoil(priceFinish) {
		return m.inContainer(cid, scryfallID, "foil") ||
			m.inContainer(cid, scryfallID, "etched")
	}
	return m.inContainer(cid, scryfallID, "nonfoil")
}

// containerEligible reports whether the container row at index i can be
// selected on the current view. All cards always can; views that set no
// eligibility set accept everything.
func (m Model) containerEligible(i int) bool {
	if i < 0 || i >= len(m.containers) {
		return false
	}
	c := m.containers[i]
	if c.Kind == kindAllCards || m.viewEligible == nil {
		return true
	}
	return m.viewEligible[c.ID]
}

// eligibleContainers marks every container for which holds answers yes —
// the grey-out set for views where an empty selection would be a dead end.
func (m Model) eligibleContainers(holds func(cid int64) bool) map[int64]bool {
	out := make(map[int64]bool, len(m.containers))
	for _, c := range m.containers {
		if c.Kind == kindAllCards {
			continue
		}
		if holds(c.ID) {
			out[c.ID] = true
		}
	}
	return out
}

// viewSkips reports whether the current view greys ineligible containers,
// which is also the views whose cursor skips them.
func (m Model) viewSkips() bool {
	return m.view == viewUnpriced || m.view == viewWatches
}

// viewScope names the selected container when it narrows the view — the
// suffix the analytical headers carry so a filtered table says so.
func (m Model) viewScope() string {
	if sel := m.selectedContainer(); sel != nil && sel.Kind != kindAllCards {
		return " · " + strings.ToUpper(sel.Name)
	}
	return ""
}
