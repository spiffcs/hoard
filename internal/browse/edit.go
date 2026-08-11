package browse

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
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
		return false, "editing works on holdings · press v to come back"
	}
	sel := m.selectedContainer()
	if sel == nil {
		return false, ""
	}
	if sel.Kind == kindAllCards {
		return false, "this list merges every container · edit the card in its binder or deck"
	}
	// A set row's cards are editable, but not through here: the selected
	// container's id is synthetic, so the verbs resolve each row back to its
	// binders and branch before this gate (see setsmode.go). This stays as
	// the backstop for any caller that does not.
	if sel.Kind == kindSet {
		return false, "this list is every printing from " + sel.Name + " · edit the card in its binder or deck"
	}
	if sel.Kind != store.KindCollection {
		return false, "deck cards are owned by the imported list; edit the binder instead"
	}
	return true, ""
}

// adjustQuantity changes the selected holding by delta.
func (m *Model) adjustQuantity(delta int) {
	// A set row has no container to edit through — it resolves to whichever
	// binders hold the printing.
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
	binderID := m.selectedContainer().ID
	previous, err := m.store.SetHoldingQuantityIn(binderID, c.ScryfallID, c.Finish, c.Condition, want)
	if err != nil {
		m.setError(err)
		return
	}

	id, finish, cond, name := c.ScryfallID, c.Finish, c.Condition, c.Name
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetHoldingQuantityIn(binderID, id, finish, cond, previous)
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

// heldEditable is the held row under the detail's cursor when the overlay
// can edit it, explaining the refusal when it cannot — the detail's
// editable(). Deck rows are owned by their imported lists, exactly as on
// the holdings pane.
func (m *Model) heldEditable() (store.Holding, bool) {
	d := m.detail
	if d == nil || len(d.holdings) == 0 {
		m.status, m.statusErr = "you hold no copies of this card", true
		return store.Holding{}, false
	}
	h := d.holdings[min(max(d.heldCursor, 0), len(d.holdings)-1)]
	if h.ContainerKind != store.KindCollection {
		m.status, m.statusErr = "deck cards are owned by the imported list; edit the binder instead", true
		return store.Holding{}, false
	}
	return h, true
}

// adjustHeldQuantity changes the held row under the detail cursor by
// delta — the overlay's +/-. Seeing the real count is exactly when a wrong
// one gets noticed, so the edit lives where the reader already is.
func (m *Model) adjustHeldQuantity(delta int) tea.Cmd {
	h, ok := m.heldEditable()
	if !ok {
		return nil
	}
	return m.setHeldQuantity(h, max(h.Quantity+delta, 0), m.detail.card.Name)
}

// editHeldField opens the edit prompt for the held row's highlighted
// field — enter in the overlay's held zone. Each field asks its own
// question: a new count, a new set code, a new binder.
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

// promptHeldQuantity asks for the held row's new count, prefilled with
// the current one.
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

// parseQuantity reads a held count: a plain whole number, zero removing.
func parseQuantity(text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 || n > 9999 {
		return 0, fmt.Errorf("say a whole number of copies, like 3 (0 removes)")
	}
	return n, nil
}

// setHeldQuantity commits a held row's new count, undo included — the
// shared tail of the overlay's +/- and its quantity prompt. The returned
// command is reloadDetail's comp refetch, and dropping it is what left the
// COMPS section on "reading today's vendor quotes…" with no read in flight.
func (m *Model) setHeldQuantity(h store.Holding, want int, name string) tea.Cmd {
	if want == h.Quantity {
		return nil
	}
	previous, err := m.store.SetHoldingQuantityIn(h.ContainerID, h.ScryfallID, h.Finish, h.Condition, want)
	if err != nil {
		m.setError(err)
		return nil
	}
	cid, id, finish, cond := h.ContainerID, h.ScryfallID, h.Finish, h.Condition
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetHoldingQuantityIn(cid, id, finish, cond, previous)
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

// promptHeldFinish asks for the held row's finish — the fix for copies
// recorded in the wrong one (an import that missed a foil marker). The
// vocabulary is the enum the database speaks: - (plain nonfoil), foil,
// etched.
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

// parseFinish reads a finish: the stored enum, with the display dash (or
// nothing) accepted for plain nonfoil.
func parseFinish(text string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "-", "", "nonfoil", "non-foil":
		return "nonfoil", nil
	case "foil":
		return "foil", nil
	case "etched":
		return "etched", nil
	}
	return "", fmt.Errorf("a finish is - (plain), foil, or etched")
}

// moveHeldFinish re-keys the held row's finish, merging with copies already
// held in the target finish, undo included. The overlay's cursor follows
// the row to its new finish, and the links refresh with it — Card
// Kingdom's page is per finish.
func (m *Model) moveHeldFinish(h store.Holding, name, want string) tea.Cmd {
	if want == h.Finish {
		m.status, m.statusErr = "already "+ui.Finish(h.Finish), false
		return nil
	}
	prevTarget, err := m.store.MoveEntryFinish(h.ContainerID, h.ScryfallID, h.Finish, want, h.Condition)
	if err != nil {
		m.setError(err)
		return nil
	}
	cid, id, from, qty, cond := h.ContainerID, h.ScryfallID, h.Finish, h.Quantity, h.Condition
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s %s", name, ui.Finish(from)),
		undo: func(st Editor) error {
			if _, err := st.SetHoldingQuantityIn(cid, id, want, cond, prevTarget); err != nil {
				return err
			}
			_, err := st.SetHoldingQuantityIn(cid, id, from, cond, qty)
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

// promptHeldCondition asks what condition the held row's copies are in — the
// one fact about a card that hoard cannot learn for itself. A camera cannot
// judge wear, and no feed carries it, so this prompt is where an assessment
// enters the hoard at all.
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

// conditionInput prefills the prompt. An unassessed row starts empty rather
// than with the word "unknown": the field is being asked to state something,
// and the commonest answer is a grade, not a re-assertion that nobody knows.
func conditionInput(condition string) string {
	if condition == "" || condition == store.ConditionUnknown {
		return ""
	}
	return condition
}

// parseCondition reads a condition: the stored vocabulary, the words the
// marketplaces spell it with, and the display dash (or nothing) for unassessed.
//
// It deliberately accepts more than it stores. Somebody typing here has just
// read "Lightly Played" off a Moxfield page or a seller's listing, and refusing
// the words in favour of the two-letter code would be pedantry — the same
// generosity normCondition shows a CSV.
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

// moveHeldCondition re-keys the held row's condition, merging with copies
// already held in the target condition, undo included. The mirror of
// moveHeldFinish, down to the two-step undo: the merge destroyed a quantity
// that has to come back before the source row does.
func (m *Model) moveHeldCondition(h store.Holding, name, want string) tea.Cmd {
	from := h.Condition
	if from == "" {
		from = store.ConditionUnknown
	}
	if want == from {
		m.status, m.statusErr = "already "+ui.Condition(from), false
		return nil
	}
	prevTarget, err := m.store.MoveEntryCondition(h.ContainerID, h.ScryfallID, h.Finish, from, want)
	if err != nil {
		m.setError(err)
		return nil
	}
	cid, id, finish, qty := h.ContainerID, h.ScryfallID, h.Finish, h.Quantity
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s %s", name, ui.Condition(from)),
		undo: func(st Editor) error {
			if _, err := st.SetHoldingQuantityIn(cid, id, finish, want, prevTarget); err != nil {
				return err
			}
			_, err := st.SetHoldingQuantityIn(cid, id, finish, from, qty)
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

// promptHeldSet asks which set the held row should be attributed to — the
// fix for a printing that resolved to the wrong set on import (a name-only
// decklist line lands on whatever printing Scryfall answers with).
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

// repointHeldSet moves the held row onto the named set's printing of the
// same card, re-pointing the overlay at the result.
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
	prevTarget, err := m.store.MoveEntry(h.ContainerID, h.ScryfallID, h.Finish, h.Condition, h.ContainerID, pick.ID)
	if err != nil {
		m.setError(err)
		return nil
	}
	cid, fromID, toID, finish, qty, cond := h.ContainerID, h.ScryfallID, pick.ID, h.Finish, h.Quantity, h.Condition
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s → %s", name, strings.ToUpper(code)),
		undo: func(st Editor) error {
			if _, err := st.SetHoldingQuantityIn(cid, toID, finish, cond, prevTarget); err != nil {
				return err
			}
			_, err := st.SetHoldingQuantityIn(cid, fromID, finish, cond, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("%s is now %s in %s",
		name, ui.Printing(pick.Set, pick.CollectorNumber), h.ContainerName)
	m.statusErr = false
	m.refresh()
	// reloadDetail's comp fetch answers the printing the overlay showed;
	// loadPrinting below re-points at the corrected one and marks its comps
	// pending, so that fetch must run too — dropping either left the COMPS
	// section pending forever on whichever printing lost its command.
	var cmds []tea.Cmd
	cmds = append(cmds, m.reloadDetail())
	// The overlay follows the corrected printing, art included.
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

// lowestPrintIn picks the set's printing with the lowest collector number
// — deterministic; ok=false when the set never printed the card.
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

// collectorLess orders collector numbers numerically where they are
// numbers, falling back to the string for suffixed forms ("142a").
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

// promptHeldLocation asks which binder the held row should live in.
func (m *Model) promptHeldLocation() {
	h, ok := m.heldEditable()
	if !ok {
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

// moveHeldTo moves the held row into another binder, merging with copies
// already there.
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
	// The reserved aliases keep meaning the default binder after a rename,
	// matching what `--binder Binder` and imports resolve to.
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
	prevTarget, err := m.store.MoveEntry(h.ContainerID, h.ScryfallID, h.Finish, h.Condition, target.ID, h.ScryfallID)
	if err != nil {
		m.setError(err)
		return nil
	}
	fromC, toC, sid, finish, qty, cond := h.ContainerID, target.ID, h.ScryfallID, h.Finish, h.Quantity, h.Condition
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s → %s", name, target.Name),
		undo: func(st Editor) error {
			if _, err := st.SetHoldingQuantityIn(toC, sid, finish, cond, prevTarget); err != nil {
				return err
			}
			_, err := st.SetHoldingQuantityIn(fromC, sid, finish, cond, qty)
			return err
		},
	})
	m.status = fmt.Sprintf("moved %s (%s) ×%d to %s", name, finish, qty, target.Name)
	m.statusErr = false
	m.refresh()
	return m.reloadDetail()
}

// askHeldRemoval stages the removal of the held row under the detail
// cursor — y/n first, like every other remove.
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

// removeHeld zeroes the confirmed held row and records the undo.
func (m *Model) removeHeld(h store.Holding, name string) tea.Cmd {
	previous, err := m.store.SetHoldingQuantityIn(h.ContainerID, h.ScryfallID, h.Finish, h.Condition, 0)
	if err != nil {
		m.setError(err)
		return nil
	}
	cid, id, finish, cond := h.ContainerID, h.ScryfallID, h.Finish, h.Condition
	m.undoable(undoAction{
		desc: fmt.Sprintf("%s ×%d", name, previous),
		undo: func(st Editor) error {
			_, err := st.SetHoldingQuantityIn(cid, id, finish, cond, previous)
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

// removeDeck deletes the selected deck.
func (m *Model) removeDeck() {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind == store.KindCollection {
		m.status, m.statusErr = "a binder cannot be removed here", true
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

// refresh, the re-read every edit here ends with, lives in reread.go beside
// the r key's reload — the two used to be separate bodies that disagreed
// about what a re-read costs the reader.
