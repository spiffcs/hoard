package browse

import (
	"github.com/spiffcs/hoard/internal/finish"
	"strings"
)

func entryKey(scryfallID, finish string) string {
	return scryfallID + "|" + finish
}

func (m *Model) rebuildEntryIndex() error {
	keys, err := m.store.EntryKeys()
	if err != nil {
		m.entryIndex = nil
		return err
	}
	idx := make(map[int64]map[string]int)
	for _, k := range keys {
		set := idx[k.ContainerID]
		if set == nil {
			set = make(map[string]int)
			idx[k.ContainerID] = set
		}
		set[entryKey(k.ScryfallID, k.Finish.String())] += k.Quantity
	}
	m.entryIndex = idx
	return nil
}

func (m Model) filterContainerID() (int64, bool) {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind == kindAllCards || sel.Kind == kindSet {
		return 0, false
	}
	return sel.ID, true
}

func (m Model) filterSetCode() (string, bool) {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind != kindSet {
		return "", false
	}
	return sel.setCode, true
}

func (m Model) inContainer(cid int64, scryfallID string, fin finish.Finish) bool {
	return m.containerQty(cid, scryfallID, fin) > 0
}

func (m Model) containerQty(cid int64, scryfallID string, fin finish.Finish) int {
	return m.entryIndex[cid][entryKey(scryfallID, fin.String())]
}

func (m Model) inContainerPriced(cid int64, scryfallID string, priceFinish finish.Finish) bool {
	return m.containerQtyPriced(cid, scryfallID, priceFinish) > 0
}

func (m Model) containerQtyPriced(cid int64, scryfallID string, priceFinish finish.Finish) int {
	if priceFinish.UsesFoilPricing() {
		return m.containerQty(cid, scryfallID, finish.Foil) +
			m.containerQty(cid, scryfallID, finish.Etched)
	}
	return m.containerQty(cid, scryfallID, finish.Nonfoil)
}

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

func (m Model) eligibleSets(holds func(setCode string) bool) map[int64]bool {
	out := make(map[int64]bool, len(m.containers))
	for _, c := range m.containers {
		if c.Kind != kindSet {
			continue
		}
		if holds(c.setCode) {
			out[c.ID] = true
		}
	}
	return out
}

func (m Model) viewSkips() bool {
	return m.view == viewWatches
}

func (m Model) viewScope() string {
	if sel := m.selectedContainer(); sel != nil && sel.Kind != kindAllCards {
		return " · " + strings.ToUpper(sel.Name)
	}
	return ""
}
