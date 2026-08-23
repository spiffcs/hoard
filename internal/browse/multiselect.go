package browse

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const noSelection = -1

func (m Model) selectionRange() (lo, hi int, ok bool) {
	if m.selAnchor == noSelection || m.view != viewHoldings || len(m.cards) == 0 {
		return 0, 0, false
	}
	cur := min(max(m.cursor[paneCards], 0), len(m.cards)-1)
	anchor := min(max(m.selAnchor, 0), len(m.cards)-1)
	return min(anchor, cur), max(anchor, cur), true
}

func (m Model) selectedRows() []store.CollectionRow {
	lo, hi, ok := m.selectionRange()
	if !ok {
		return nil
	}
	out := make([]store.CollectionRow, 0, hi-lo+1)
	for _, c := range m.cards[lo : hi+1] {
		r := store.CollectionRow{Finish: c.Finish, Condition: c.Condition,
			Quantity: c.Quantity, Value: c.Value}
		r.ScryfallID, r.Name = c.ScryfallID, c.Name
		r.SetCode, r.CollectorNumber = c.SetCode, c.CollectorNumber
		out = append(out, r)
	}
	return out
}

func (m Model) selectedRow(i int) bool {
	lo, hi, ok := m.selectionRange()
	return ok && i >= lo && i <= hi
}

func (m *Model) clearSelection() { m.selAnchor = noSelection }

func (m *Model) extendSelection(delta int) {
	if m.view != viewHoldings {
		return
	}
	m.focus = paneCards
	n := len(m.cards)
	if n == 0 {
		return
	}
	if m.selAnchor == noSelection {
		m.selAnchor = min(max(m.cursor[paneCards], 0), n-1)
	}
	m.cursor[paneCards] = min(max(m.cursor[paneCards]+delta, 0), n-1)
	m.scrollIntoView()
	m.status, m.statusErr = m.selectionStatus(), false
}

func (m Model) selectionTotals() (copies, rows int, value float64) {
	for _, r := range m.selectedRows() {
		copies += r.Quantity
		value += r.Value
		rows++
	}
	return copies, rows, value
}

func (m Model) selectionSummary() string {
	copies, rows, value := m.selectionTotals()
	if rows == 0 {
		return ""
	}
	return fmt.Sprintf("SELECTED %s · %s · %s",
		ui.Count(copies), ui.Money(value), ui.PluralCount(rows, "row", "rows"))
}

func (m Model) selectionStatus() string {
	if s := m.selectionSummary(); s != "" {
		return s + " · esc clears"
	}
	return ""
}

func (m *Model) toggleBinderCounted() {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind != store.KindCollection || sel.ID == allCardsID {
		m.status, m.statusErr = "only a binder can be left out of your collection", true
		return
	}
	counted := !sel.Counted
	if err := m.store.SetBinderCounted(sel.ID, counted); err != nil {
		m.setError(err)
		return
	}
	m.reload()
	if counted {
		m.status, m.statusErr = sel.Name+" counts toward your collection again", false
		return
	}
	m.status, m.statusErr = sel.Name+" is not counted toward your collection", false
}

func (c container) skipped() bool {
	return c.Kind == store.KindCollection && c.ID != allCardsID && !c.Counted
}
