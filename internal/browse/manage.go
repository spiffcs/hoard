package browse

// Watch and binder management from inside the browser: staging removals,
// the add-watch prompt, and the binder create/rename prompts. The store
// methods arrive through the Editor interface; undo pairs mirror the
// existing card/deck removals.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// askWatchRemoval stages removing one watch. Undo re-adds it — LastState
// resets, which re-arms the alert: the safe direction.
func (m *Model) askWatchRemoval(w store.WatchStatus) {
	m.confirm = &pendingConfirm{
		prompt: fmt.Sprintf("remove the %s %s watch on %s?", w.Op, ui.Money(w.Threshold), w.Display),
		help:   "y remove · any other key cancels",
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
		help:   "y remove · any other key cancels",
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
			// Creating a binder promises "switch to it", which the sets
			// pane cannot show — leave sets mode so focusContainer finds
			// the new row.
			m.setsMode = false
			m.refresh()
			m.focusContainer(id)
			// The card pane follows the selection to the new binder's empty
			// state — refresh() reloaded it for the cursor's old position,
			// and a "created" receipt over someone else's cards reads as
			// the create having gone somewhere it didn't.
			if err := m.loadCards(); err != nil {
				m.setError(err)
				return nil
			}
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
	case sel.Kind == kindAllCards:
		m.status, m.statusErr = allCardsName+" is every container merged; it has no name of its own", true
		return
	case sel.Kind == kindSet:
		m.status, m.statusErr = "sets are named by Wizards; only binders can be renamed", true
		return
	case sel.Kind != store.KindCollection:
		m.status, m.statusErr = "decks are named by their imported list", true
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
			// The selection scopes the analytical views, so a programmatic
			// jump re-derives like a cursor move would.
			m.deriveView()
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
		// Only when the hand is on the card pane: with the cursor in the
		// container list, "this card" would be whichever row happened to
		// sit under the other pane's cursor.
		if m.focus != paneCards {
			return nil
		}
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
	case viewMarket:
		if c := m.selectedComp(); c != nil {
			ref := &subjectRef{scryfallID: c.Card.ScryfallID, name: c.Card.Name, finish: c.Card.Finish}
			if c.HasMarket {
				price := c.Market
				ref.price = &price
			} else if c.Low > 0 {
				price := c.Low
				ref.price = &price
			}
			return ref
		}
		i := m.cursor[paneCards]
		if i >= 0 && i < len(m.marketRows) {
			r := m.marketRows[i]
			ref := &subjectRef{scryfallID: r.Card.ScryfallID, name: r.Card.Name, finish: r.Card.Finish}
			if r.HasMarket {
				// The sales-derived anchor is the row's own idea of the
				// price, so a bare threshold infers direction from it.
				price := r.Market
				ref.price = &price
			}
			return ref
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

	label := fmt.Sprintf("watch %s (%s) · threshold", sub.name, finish)
	if sub.price != nil {
		label = fmt.Sprintf("watch %s (%s) · now %s · threshold",
			sub.name, finish, ui.Money(*sub.price))
	}
	sid, name, price := sub.scryfallID, sub.name, sub.price
	// Editing an existing watch starts from its current threshold — AddWatch
	// is an upsert, and making the user retype what they are adjusting turns
	// an edit into a memory test.
	prefill := ""
	if m.view == viewWatches {
		if w := m.selectedWatch(); w != nil && w.ScryfallID == sid && w.Finish == finish {
			prefill = w.Op + " " + strconv.FormatFloat(w.Threshold, 'f', -1, 64)
		}
	}
	m.prompt = &prompt{
		label:    label,
		text:     prefill,
		help:     thresholdHelp,
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
			return "", 0, fmt.Errorf("no current price · say under %s or over %s", t, t)
		}
		op = "under"
		if n > *price {
			op = "over"
		}
	}
	return op, n, nil
}

// promptWatchByName chains two prompts — card name, then threshold — and
// runs the resolve-and-add as an operation, since pinning the printing
// needs the network. Direction must be explicit ("under 40" / "over 40"):
// there is no current price to infer from before the name resolves.
func (m *Model) promptWatchByName() {
	m.prompt = &prompt{
		label: "watch which card (name)",
		validate: func(text string) error {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("name a card")
			}
			return nil
		},
		commit: func(m *Model, name string) tea.Cmd {
			name = strings.TrimSpace(name)
			m.prompt = &prompt{
				label: fmt.Sprintf("watch %s · threshold", name),
				help:  "under 40 / over 40 (direction required; no current price to infer from) · enter accept · esc cancel",
				validate: func(text string) error {
					_, _, err := parseThreshold(text, nil)
					return err
				},
				commit: func(m *Model, text string) tea.Cmd {
					op, threshold, err := parseThreshold(text, nil)
					if err != nil {
						m.status, m.statusErr = err.Error(), true
						return nil
					}
					fn := m.opWatchAdd
					return m.startOp("adding watch", func(ctx context.Context, p progress.Fn) (string, error) {
						return fn(ctx, p, name, op, threshold)
					})
				},
			}
			return nil
		},
	}
}

// thresholdHelp spells out the watch threshold syntax, shown while either
// threshold prompt is open.
const thresholdHelp = "under 40 / over 40 set the direction · a bare 40 infers it from the current price · enter accept · esc cancel"

// startWatchPick begins choosing a watch target from the cards already
// held: jump to holdings with the filter bar open, and the next enter on a
// card runs the ordinary watch prompt — the same flow as pressing w on it,
// with the current price known so a bare threshold can infer direction.
func (m *Model) startWatchPick() tea.Cmd {
	m.detail = nil // the pick browses the panes; a detail would hide them
	cmd := m.showView(viewHoldings)
	m.focus = paneCards
	m.filtering = true
	m.watchPick = true
	return cmd
}

// finishWatchPick runs the watch prompt for the picked card, and — because
// the flow started from the watches view — jumps back there once the watch
// lands or the prompt is escaped, so the reader ends where they began. A
// refusal stays put: the error belongs where the user still is.
func (m *Model) finishWatchPick() {
	m.watchPick = false
	m.promptWatch()
	if m.prompt == nil {
		return
	}
	inner := m.prompt.commit
	m.prompt.commit = func(m *Model, text string) tea.Cmd {
		cmd := inner(m, text)
		if m.statusErr {
			return cmd
		}
		return tea.Batch(cmd, m.showView(viewWatches))
	}
	m.prompt.cancel = func(m *Model) tea.Cmd {
		cmd := m.showView(viewWatches)
		m.status, m.statusErr = "watch cancelled", false
		return cmd
	}
}
