package browse

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type undoAction struct {
	desc string

	undo func(Editor) error
}

func (m Model) editable() (bool, string) {

	if m.view != viewHoldings {
		return false, "editing works on holdings · press v to come back"
	}
	sel := m.selectedContainer()
	if sel == nil {
		return false, ""
	}
	if sel.Kind == kindAllCards {
		return false, "this list merges every container · edit the card in its binder or deck"
	}

	if sel.Kind == kindSet {
		return false, "this list is every printing from " + sel.Name + " · edit the card in its binder or deck"
	}
	if sel.Kind == kindFolder {
		return false, sel.Name + " groups decks · edit the card in the deck that holds it"
	}
	if !holdsCards(*sel) {
		return false, sel.Name + " holds no cards of its own"
	}
	return true, ""
}

func (m *Model) adjustQuantity(delta int) {

	if sel := m.selectedContainer(); m.view == viewHoldings && sel != nil && sel.Kind == kindSet {
		m.adjustSetQuantity(delta)
		return
	}
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
	ref := store.EntryRef{ContainerID: m.selectedContainer().ID, ScryfallID: c.ScryfallID,
		Finish: c.Finish, Condition: c.Condition, Board: c.Board}
	previous, err := m.store.SetEntryQuantity(ref, want)
	if err != nil {
		m.setError(err)
		return
	}

	finish, name := c.Finish, c.Name
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetEntryQuantity(ref, previous)
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

	removed, err := m.store.RemoveFromBinder(m.selectedContainer().ID, c.ScryfallID)
	if err != nil {
		m.setError(err)
		return
	}
	id, name := c.ScryfallID, c.Name
	m.undoable(undoAction{
		desc: name,
		undo: func(st Editor) error { return st.RestoreHoldings(id, removed) },
	})
	m.status = fmt.Sprintf("removed %s from %s", name, m.selectedContainer().Name)
	m.statusErr = false
	m.refresh()
}

func (m *Model) heldEditable() (store.Holding, bool) {
	d := m.detail
	if d == nil || len(d.holdings) == 0 {
		m.status, m.statusErr = "you hold no copies of this card", true
		return store.Holding{}, false
	}
	h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
	if !editableKind(h.ContainerKind) {
		m.status, m.statusErr = h.ContainerName+" holds no cards of its own", true
		return store.Holding{}, false
	}
	return h, true
}

func (m *Model) adjustHeldQuantity(delta int) tea.Cmd {
	h, ok := m.heldEditable()
	if !ok {
		return nil
	}
	return m.setHeldQuantity(h, max(h.Quantity+delta, 0), m.detail.card.Name)
}

func (m *Model) editHeldField() {
	switch m.detail.heldField {
	case fieldSet:
		m.promptHeldSet()
	case fieldFinish:
		m.promptHeldFinish()
	case fieldCondition:
		m.promptHeldCondition()
	case fieldWhere:
		m.promptHeldLocation()
	default:
		m.promptHeldQuantity()
	}
}

func (m *Model) promptHeldQuantity() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	name := m.detail.card.Name
	m.prompt = &prompt{
		label:    fmt.Sprintf("%s in %s · quantity", name, h.ContainerName),
		text:     strconv.Itoa(h.Quantity),
		help:     "a whole number · 0 removes · enter accept · esc cancel",
		validate: func(text string) error { _, err := parseQuantity(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			want, err := parseQuantity(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			return m.setHeldQuantity(h, want, name)
		},
	}
}

func parseQuantity(text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 || n > 9999 {
		return 0, fmt.Errorf("say a whole number of copies, like 3 (0 removes)")
	}
	return n, nil
}

func (m *Model) setHeldQuantity(h store.Holding, want int, name string) tea.Cmd {
	if want == h.Quantity {
		return nil
	}
	ref := h.Ref()
	previous, err := m.store.SetEntryQuantity(ref, want)
	if err != nil {
		m.setError(err)
		return nil
	}
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetEntryQuantity(ref, previous)
			return err
		},
	})
	if want == 0 {
		m.status = fmt.Sprintf("removed %s (%s) from %s", name, h.Finish, h.ContainerName)
	} else {
		m.status = fmt.Sprintf("%s (%s) ×%d in %s", name, h.Finish, want, h.ContainerName)
	}
	m.statusErr = false
	m.refresh()
	cmd := m.reloadDetail()
	if want == 0 {
		m.closeDetailIfUnheld()
	}
	return cmd
}

func (m *Model) promptHeldFinish() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	name := m.detail.card.Name
	m.prompt = &prompt{
		label:    fmt.Sprintf("%s in %s · finish", name, h.ContainerName),
		text:     ui.Finish(h.Finish),
		help:     "- plain · foil · etched · enter accept · esc cancel",
		validate: func(text string) error { _, err := parseFinish(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			want, err := parseFinish(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			return m.moveHeldFinish(h, name, want)
		},
	}
}

func parseFinish(text string) (finish.Finish, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "-", "", "nonfoil", "non-foil":
		return finish.Nonfoil, nil
	case "foil":
		return finish.Foil, nil
	case "etched":
		return finish.Etched, nil
	}
	return finish.Finish{}, fmt.Errorf("a finish is - (plain), foil, or etched")
}

func (m *Model) moveHeldFinish(h store.Holding, name string, want finish.Finish) tea.Cmd {
	if want == h.Finish {
		m.status, m.statusErr = "already "+ui.Finish(h.Finish), false
		return nil
	}
	ref := h.Ref()
	prevTarget, err := m.store.MoveEntryFinish(ref, want)
	if err != nil {
		m.setError(err)
		return nil
	}
	moved := ref
	moved.Finish = want
	cid, id, from, qty := h.ContainerID, h.ScryfallID, h.Finish, h.Quantity
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s %s", name, ui.Finish(from)),
		undo: func(st Editor) error {
			if _, err := st.SetEntryQuantity(moved, prevTarget); err != nil {
				return err
			}
			_, err := st.SetEntryQuantity(ref, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("%s (%s → %s) in %s", name, ui.Finish(from), ui.Finish(want), h.ContainerName)
	m.statusErr = false
	m.refresh()
	cmd := m.reloadDetail()
	if d := m.detail; d != nil {
		for i, held := range d.holdings {
			if held.ScryfallID == id && held.ContainerID == cid && held.Finish == want {
				d.heldCursor = i
				break
			}
		}
		m.refreshLinks(d)
	}
	return cmd
}

func (m *Model) promptHeldCondition() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	name := m.detail.card.Name
	m.prompt = &prompt{
		label:    fmt.Sprintf("%s in %s · condition", name, h.ContainerName),
		text:     conditionInput(h.Condition),
		help:     "nm · lp · mp · hp · dmg · - unknown · enter accept · esc cancel",
		validate: func(text string) error { _, err := parseCondition(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			want, err := parseCondition(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			return m.moveHeldCondition(h, name, want)
		},
	}
}

func conditionInput(condition string) string {
	if condition == "" || condition == store.ConditionUnknown {
		return ""
	}
	return condition
}

func parseCondition(text string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "-", "", "unknown", "?":
		return store.ConditionUnknown, nil
	case "nm", "near mint", "near-mint", "mint", "m":
		return store.ConditionNM, nil
	case "lp", "lightly played", "light played", "slightly played", "sp",
		"excellent", "good":
		return store.ConditionLP, nil
	case "mp", "moderately played", "played":
		return store.ConditionMP, nil
	case "hp", "heavily played":
		return store.ConditionHP, nil
	case "dmg", "d", "damaged", "poor":
		return store.ConditionDamaged, nil
	}
	return "", fmt.Errorf("a condition is nm, lp, mp, hp, dmg, or - for unknown")
}

func (m *Model) moveHeldCondition(h store.Holding, name, want string) tea.Cmd {
	from := h.Condition
	if from == "" {
		from = store.ConditionUnknown
	}
	if want == from {
		m.status, m.statusErr = "already "+ui.Condition(from), false
		return nil
	}
	ref := h.Ref()
	ref.Condition = from
	prevTarget, err := m.store.MoveEntryCondition(ref, want)
	if err != nil {
		m.setError(err)
		return nil
	}
	moved := ref
	moved.Condition = want
	cid, id, finish, qty := h.ContainerID, h.ScryfallID, h.Finish, h.Quantity
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s %s", name, ui.Condition(from)),
		undo: func(st Editor) error {
			if _, err := st.SetEntryQuantity(moved, prevTarget); err != nil {
				return err
			}
			_, err := st.SetEntryQuantity(ref, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("%s (%s → %s) in %s",
		name, ui.Condition(from), ui.Condition(want), h.ContainerName)
	m.statusErr = false
	m.refresh()
	cmd := m.reloadDetail()
	if d := m.detail; d != nil {
		for i, held := range d.holdings {
			if held.ScryfallID == id && held.ContainerID == cid &&
				held.Finish == finish && held.Condition == want {
				d.heldCursor = i
				break
			}
		}
	}
	return cmd
}

func (m *Model) promptHeldSet() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	if m.printSearch == nil {
		m.status, m.statusErr = "set lookup not available", true
		return
	}
	name := m.detail.card.Name
	m.prompt = &prompt{
		label: fmt.Sprintf("%s · new set code", name),
		text:  h.SetCode,
		help:  "a set code, like md1 · enter accept · esc cancel",
		validate: func(text string) error {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("say a set code, like md1")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd { return m.repointHeldSet(h, name, text) },
	}
}

func (m *Model) repointHeldSet(h store.Holding, name, text string) tea.Cmd {
	code := strings.ToLower(strings.TrimSpace(text))
	if strings.EqualFold(code, h.SetCode) {
		m.status, m.statusErr = "already the "+strings.ToUpper(code)+" printing", false
		return nil
	}
	prints, err := m.printSearch(m.ctx, name)
	if err != nil {
		m.setError(err)
		return nil
	}
	pick, ok := lowestPrintIn(prints, code)
	if !ok {
		m.status, m.statusErr = fmt.Sprintf("no %s printing in %s", name, strings.ToUpper(code)), true
		return nil
	}
	if err := m.store.UpsertPrintings([]scryfall.Card{pick}); err != nil {
		m.setError(err)
		return nil
	}
	ref := h.Ref()
	prevTarget, err := m.store.MoveEntry(ref, ref.ContainerID, pick.ID)
	if err != nil {
		m.setError(err)
		return nil
	}
	moved := ref
	moved.ScryfallID = pick.ID
	cid, toID, finish, qty := h.ContainerID, pick.ID, h.Finish, h.Quantity
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s → %s", name, strings.ToUpper(code)),
		undo: func(st Editor) error {
			if _, err := st.SetEntryQuantity(moved, prevTarget); err != nil {
				return err
			}
			_, err := st.SetEntryQuantity(ref, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("%s is now %s in %s",
		name, ui.Printing(pick.Set, pick.CollectorNumber), h.ContainerName)
	m.statusErr = false
	m.refresh()

	var cmds []tea.Cmd
	cmds = append(cmds, m.reloadDetail())

	d := m.detail
	if d == nil {
		return tea.Batch(cmds...)
	}
	for i, held := range d.holdings {
		if held.ScryfallID == toID && held.ContainerID == cid && held.Finish == finish {
			d.heldCursor = i
			break
		}
	}
	if m.loadPrinting(d, toID) {
		m.refreshLinks(d)
		cmds = append(cmds, m.fetchDetailComps(toID))
		if cmd := m.fetchDetailImage(); cmd != nil {
			cmds = append(cmds, cmd)
		} else {
			d.image = nil
		}
	}
	return tea.Batch(cmds...)
}

func lowestPrintIn(prints []scryfall.Card, setCode string) (scryfall.Card, bool) {
	var best scryfall.Card
	found := false
	for _, p := range prints {
		if !strings.EqualFold(p.Set, setCode) {
			continue
		}
		if !found || collectorLess(p.CollectorNumber, best.CollectorNumber) {
			best, found = p, true
		}
	}
	return best, found
}

func collectorLess(a, b string) bool {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return an < bn
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a < b
}

func (m *Model) promptHeldLocation() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	if h.ContainerKind == store.KindDeck {
		m.status, m.statusErr = "a deck card moves with its list · change its quantity here instead", true
		return
	}
	name := m.detail.card.Name
	m.prompt = &prompt{
		label: fmt.Sprintf("%s · move to which binder", name),
		text:  h.ContainerName,
		help:  "a binder's name · enter accept · esc cancel",
		validate: func(text string) error {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("name a binder")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd { return m.moveHeldTo(h, name, text) },
	}
}

func (m *Model) moveHeldTo(h store.Holding, name, text string) tea.Cmd {
	want := strings.TrimSpace(text)
	if strings.EqualFold(want, h.ContainerName) {
		m.status, m.statusErr = "already in "+h.ContainerName, false
		return nil
	}
	binders, err := m.store.ListBinders()
	if err != nil {
		m.setError(err)
		return nil
	}
	var target *store.DeckSummary
	for i := range binders {
		if strings.EqualFold(binders[i].Name, want) {
			target = &binders[i]
			break
		}
	}

	if target == nil && store.IsReservedBinderName(want) {
		for i := range binders {
			if binders[i].IsDefault {
				target = &binders[i]
				break
			}
		}
	}
	if target == nil {
		m.status, m.statusErr = fmt.Sprintf("no binder named %q", want), true
		return nil
	}
	ref := h.Ref()
	prevTarget, err := m.store.MoveEntry(ref, target.ID, ref.ScryfallID)
	if err != nil {
		m.setError(err)
		return nil
	}
	moved := ref
	moved.ContainerID = target.ID
	finish, qty := h.Finish, h.Quantity
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s → %s", name, target.Name),
		undo: func(st Editor) error {
			if _, err := st.SetEntryQuantity(moved, prevTarget); err != nil {
				return err
			}
			_, err := st.SetEntryQuantity(ref, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("moved %s (%s) ×%d to %s", name, finish, qty, target.Name)
	m.statusErr = false
	m.refresh()
	return m.reloadDetail()
}

func (m *Model) askHeldRemoval() {
	h, ok := m.heldEditable()
	if !ok {
		return
	}
	name := m.detail.card.Name
	m.confirm = &pendingConfirm{
		help: "y remove · any other key cancels",
		prompt: fmt.Sprintf("remove %s (%s) ×%d from %s?",
			name, h.Finish, h.Quantity, h.ContainerName),
		onYes: func(m *Model) tea.Cmd { return m.removeHeld(h, name) },
	}
}

func (m *Model) removeHeld(h store.Holding, name string) tea.Cmd {
	ref := h.Ref()
	previous, err := m.store.SetEntryQuantity(ref, 0)
	if err != nil {
		m.setError(err)
		return nil
	}
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetEntryQuantity(ref, previous)
			return err
		},
	})
	m.status = fmt.Sprintf("removed %s (%s) from %s", name, h.Finish, h.ContainerName)
	m.statusErr = false
	m.refresh()
	cmd := m.reloadDetail()
	m.closeDetailIfUnheld()
	return cmd
}

func (m *Model) removeDeck() {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind == store.KindCollection {
		m.status, m.statusErr = "a binder cannot be removed here", true
		return
	}

	views, err := m.store.DeckEntries(sel.ID)
	if err != nil {
		m.setError(err)
		return
	}
	entries := make([]store.Entry, 0, len(views))
	for _, v := range views {
		entries = append(entries, store.Entry{
			ScryfallID: v.Card.ScryfallID, Finish: v.Finish,
			Condition: v.Condition, Board: v.Board, Quantity: v.Quantity,
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

			_, err := st.UpsertDeck(meta, entries)
			return err
		},
	})
	m.status = fmt.Sprintf("removed deck %s", name)
	m.statusErr = false
	m.refresh()
}

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

func (m *Model) undoable(a undoAction) { m.undoStack = &a }
