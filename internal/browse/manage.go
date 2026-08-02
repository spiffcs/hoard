package browse

// Watch and binder management from inside the browser: staging removals,
// the add-watch prompt, and the binder create/rename prompts. The store
// methods arrive through the Editor interface; undo pairs mirror the
// existing card/deck removals.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// askWatchRemoval stages removing one watch. Undo re-adds it — LastState
// resets, which re-arms the alert: the safe direction.
func (m *Model) askWatchRemoval(w store.WatchStatus) {
	m.confirm = &pendingConfirm{
		prompt: fmt.Sprintf("remove the %s %s watch on %s?", w.Op, ui.Money(w.Threshold), w.Display),
		onYes: func(m *Model) tea.Cmd {
			if err := m.store.RemoveWatch(w.ID); err != nil {
				m.setError(err)
				return nil
			}
			m.undoable(undoAction{
				desc: fmt.Sprintf("watch on %s", w.Display),
				undo: func(e Editor) error {
					return e.AddWatch(w.ScryfallID, w.Display, w.Finish, w.Op, w.Threshold)
				},
			})
			m.status, m.statusErr = "removed the watch on "+w.Display, false
			if err := m.loadView(); err != nil {
				m.setError(err)
			}
			m.clampCursor(paneCards)
			return nil
		},
	}
}

// askBinderRemoval stages removing an empty binder; the store refuses a
// non-empty one and that message lands on the status line as-is.
func (m *Model) askBinderRemoval(sel *container) {
	name := sel.Name
	id := sel.ID
	m.confirm = &pendingConfirm{
		prompt: fmt.Sprintf("remove binder %q?", name),
		onYes: func(m *Model) tea.Cmd {
			if err := m.store.DeleteBinder(id); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			// Empty by precondition, so recreating it is a faithful undo.
			m.undoable(undoAction{
				desc: "binder " + name,
				undo: func(e Editor) error {
					_, err := e.CreateBinder(name)
					return err
				},
			})
			m.status, m.statusErr = "removed binder "+name, false
			m.refresh()
			return nil
		},
	}
}

// promptNewBinder opens the create-binder prompt.
func (m *Model) promptNewBinder() {
	m.prompt = &prompt{
		label: "new binder name",
		commit: func(m *Model, text string) tea.Cmd {
			id, err := m.store.CreateBinder(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.undoable(undoAction{
				desc: "binder " + text,
				undo: func(e Editor) error { return e.DeleteBinder(id) },
			})
			m.refresh()
			m.focusContainer(id)
			m.status, m.statusErr = "created binder "+text, false
			return nil
		},
	}
}

// promptRenameBinder opens the rename prompt for the selected binder,
// prefilled with its current name.
func (m *Model) promptRenameBinder() {
	sel := m.selectedContainer()
	switch {
	case m.focus != paneContainers || sel == nil:
		m.status, m.statusErr = "select a binder to rename (tab to the left pane)", true
		return
	case sel.Kind != store.KindCollection:
		m.status, m.statusErr = "decks are named by their imported list", true
		return
	case sel.isDefault:
		m.status, m.statusErr = "the default "+strings.ToLower(store.LooseName)+" cannot be renamed", true
		return
	}
	id, was := sel.ID, sel.Name
	m.prompt = &prompt{
		label: "rename binder",
		text:  was,
		commit: func(m *Model, text string) tea.Cmd {
			if err := m.store.RenameBinder(id, text); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.undoable(undoAction{
				desc: "name " + was,
				undo: func(e Editor) error { return e.RenameBinder(id, was) },
			})
			m.refresh()
			m.status, m.statusErr = fmt.Sprintf("renamed %s to %s", was, text), false
			return nil
		},
	}
}

// focusContainer moves the left cursor to a container by id.
func (m *Model) focusContainer(id int64) {
	for i, c := range m.containers {
		if c.ID == id {
			m.cursor[paneContainers] = i
			m.scrollIntoView()
			return
		}
	}
}

// subjectRef is the printing the user is "on", wherever they are on it.
type subjectRef struct {
	scryfallID string
	name       string
	finish     string
	price      *float64
}

// subjectCard resolves the current subject: the open detail card, or the
// row under the cards cursor in whichever view carries printings.
func (m *Model) subjectCard() *subjectRef {
	if m.detail != nil {
		c := m.detail.card
		price := c.PriceUSD
		return &subjectRef{scryfallID: c.ScryfallID, name: c.Name, finish: "nonfoil", price: price}
	}
	switch m.view {
	case viewHoldings:
		if c := m.selectedCard(); c != nil {
			return &subjectRef{scryfallID: c.ScryfallID, name: c.Name, finish: c.Finish, price: c.Price}
		}
	case viewMovers:
		i := m.cursor[paneCards]
		if i >= 0 && i < len(m.movers) {
			c := m.movers[i]
			price := c.New
			return &subjectRef{scryfallID: c.ScryfallID, name: c.Name, finish: c.Finish, price: &price}
		}
	case viewWatches:
		if w := m.selectedWatch(); w != nil {
			return &subjectRef{scryfallID: w.ScryfallID, name: w.Name, finish: w.Finish, price: w.PriceUSD}
		}
	case viewUnpriced:
		i := m.cursor[paneCards]
		if i >= 0 && i < len(m.unpriced) {
			r := m.unpriced[i]
			return &subjectRef{scryfallID: r.ScryfallID, name: r.Name, finish: r.Finish}
		}
	}
	return nil
}

// promptWatch opens the add-watch prompt for the current subject.
func (m *Model) promptWatch() {
	sub := m.subjectCard()
	if sub == nil {
		m.status, m.statusErr = "select a card to watch", true
		return
	}
	if sub.finish == "etched" {
		m.status, m.statusErr = "watches support nonfoil and foil only", true
		return
	}
	finish := "nonfoil"
	if scryfall.PricedAsFoil(sub.finish) {
		finish = "foil"
	}

	label := fmt.Sprintf("watch %s (%s) — threshold", sub.name, finish)
	if sub.price != nil {
		label = fmt.Sprintf("watch %s (%s) · now %s — threshold",
			sub.name, finish, ui.Money(*sub.price))
	}
	sid, name, price := sub.scryfallID, sub.name, sub.price
	m.prompt = &prompt{
		label:    label,
		validate: func(text string) error { _, _, err := parseThreshold(text, price); return err },
		commit: func(m *Model, text string) tea.Cmd {
			op, threshold, err := parseThreshold(text, price)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			if err := m.store.AddWatch(sid, name, finish, op, threshold); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			now := ""
			if price != nil {
				now = " · now " + ui.Money(*price)
			}
			m.status = fmt.Sprintf("watching %s (%s) %s %s%s",
				name, finish, op, ui.Money(threshold), now)
			m.statusErr = false
			if m.view == viewWatches {
				if err := m.loadView(); err != nil {
					m.setError(err)
				}
			}
			return nil
		},
	}
}

// parseThreshold reads a watch threshold: "under 40", "over 40", "<40",
// ">40", or a bare number whose direction is inferred from the current
// price — below it means waiting for a dip, above it a rise. A card with no
// price refuses bare numbers: there is nothing to infer from.
func parseThreshold(text string, price *float64) (op string, threshold float64, err error) {
	t := strings.TrimSpace(strings.ToLower(text))
	switch {
	case strings.HasPrefix(t, "under "), strings.HasPrefix(t, "<"):
		op = "under"
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "under "), "<"))
	case strings.HasPrefix(t, "over "), strings.HasPrefix(t, ">"):
		op = "over"
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "over "), ">"))
	}
	t = strings.TrimPrefix(t, "$")
	n, perr := strconv.ParseFloat(t, 64)
	if perr != nil || n <= 0 {
		return "", 0, fmt.Errorf("say a price, like 40, under 40, or over 40")
	}
	if op == "" {
		if price == nil {
			return "", 0, fmt.Errorf("no current price — say under %s or over %s", t, t)
		}
		op = "under"
		if n > *price {
			op = "over"
		}
	}
	return op, n, nil
}
