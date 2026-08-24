package browse

import (
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
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
	for _, c := range m.containers {
		if c.Kind != store.KindDeck || c.parentID == 0 {
			continue
		}
		roll := idx[c.parentID]
		if roll == nil {
			roll = make(map[string]int)
			idx[c.parentID] = roll
		}
		for k, n := range idx[c.ID] {
			roll[k] += n
		}
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

func (m Model) containerIndexOf(id int64) int {
	for i, c := range m.containers {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func (m *Model) selectContainer(i int) error {
	m.cursor[paneContainers] = i
	m.scrollIntoView()
	if err := m.loadCards(); err != nil {
		return err
	}
	m.deriveView()
	return nil
}

func (m *Model) restorePick() error {
	if len(m.containers) == 0 {
		return nil
	}
	if !m.containerEligible(m.cursor[paneContainers]) {
		displaced := m.selectedContainer()
		if err := m.selectContainer(0); err != nil {
			return err
		}
		if displaced != nil && displaced.Kind != kindAllCards {
			m.displacedContainer = displaced.ID
			m.status = displaced.Name + " has no " + m.view.String() + " · showing all cards"
		}
		return nil
	}
	if m.displacedContainer == 0 {
		return nil
	}
	want := m.containerIndexOf(m.displacedContainer)
	if m.cursor[paneContainers] != 0 || want < 0 {
		m.displacedContainer = 0
		return nil
	}
	if !m.containerEligible(want) {
		return nil
	}
	m.displacedContainer = 0
	return m.selectContainer(want)
}
