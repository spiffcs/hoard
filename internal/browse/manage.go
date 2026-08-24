package browse

import (
	"context"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

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

			m.setsMode = false
			m.refresh()
			m.focusContainer(id)

			if err := m.loadCards(); err != nil {
				m.setError(err)
				return nil
			}
			m.status, m.statusErr = "created binder "+text, false
			return nil
		},
	}
}

func (m *Model) promptRename() {
	sel := m.selectedContainer()
	switch {
	case m.focus != paneContainers || sel == nil:
		m.status, m.statusErr = "select a binder, deck or folder to rename (tab to the left pane)", true
		return
	case sel.Kind == kindAllCards:
		m.status, m.statusErr = allCardsName+" is every container merged; it has no name of its own", true
		return
	case sel.Kind == kindSet:
		m.status, m.statusErr = "sets are named by Wizards; only binders, decks and folders can be renamed", true
		return
	}
	id, was, kind := sel.ID, sel.Name, sel.Kind
	noun := "binder"
	switch kind {
	case store.KindDeck:
		noun = "deck"
	case kindFolder:
		noun = "folder"
	}
	help := "enter accept · esc cancel · ctrl+u wipe"
	if kind == store.KindDeck {
		help = "the name you choose survives a refresh · " + help
	}
	m.prompt = &prompt{
		label: "rename " + noun,
		text:  was,
		help:  help,
		commit: func(m *Model, text string) tea.Cmd {
			if err := m.renameContainer(kind, id, text); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			m.undoable(undoAction{
				desc: "name " + was,
				undo: func(e Editor) error { return renameWith(e, kind, id, was) },
			})
			m.refresh()
			m.status, m.statusErr = fmt.Sprintf("renamed %s to %s", was, strings.TrimSpace(text)), false
			return nil
		},
	}
}

func (m *Model) renameContainer(kind string, id int64, name string) error {
	return renameWith(m.store, kind, id, name)
}

func renameWith(e Editor, kind string, id int64, name string) error {
	switch kind {
	case store.KindDeck:
		return e.RenameDeck(id, name)
	case kindFolder:
		return e.RenameFolder(id, name)
	}
	return e.RenameBinder(id, name)
}

func (m *Model) focusContainer(id int64) {
	for i, c := range m.containers {
		if c.ID == id {
			m.cursor[paneContainers] = i
			m.displacedContainer = 0
			m.scrollIntoView()

			m.deriveView()
			return
		}
	}
}

type subjectRef struct {
	scryfallID string
	name       string
	finish     finish.Finish
	price      *float64
}

func (m *Model) subjectCard() *subjectRef {
	if m.detail != nil {
		c := m.detail.card
		price := c.PriceUSD
		return &subjectRef{scryfallID: c.ScryfallID, name: c.Name, finish: finish.Nonfoil, price: price}
	}
	switch m.view {
	case viewHoldings:

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

		if r := m.selectedUnpricedRow(); r != nil {
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

				price := r.Market
				ref.price = &price
			}
			return ref
		}
	}
	return nil
}

func (m *Model) promptWatch() {
	sub := m.subjectCard()
	if sub == nil {
		m.status, m.statusErr = "select a card to watch", true
		return
	}
	if sub.finish == finish.Etched {
		m.status, m.statusErr = "watches support nonfoil and foil only", true
		return
	}
	fin := finish.Nonfoil
	if sub.finish.UsesFoilPricing() {
		fin = finish.Foil
	}

	label := fmt.Sprintf("watch %s (%s) · threshold", sub.name, fin)
	if sub.price != nil {
		label = fmt.Sprintf("watch %s (%s) · now %s · threshold",
			sub.name, fin, ui.Money(*sub.price))
	}
	sid, name, price := sub.scryfallID, sub.name, sub.price

	prefill := ""
	if m.view == viewWatches {
		if w := m.selectedWatch(); w != nil && w.ScryfallID == sid && w.Finish == fin {
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
			if err := m.store.AddWatch(sid, name, fin, op, threshold); err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			now := ""
			if price != nil {
				now = " · now " + ui.Money(*price)
			}
			m.status = fmt.Sprintf("watching %s (%s) %s %s%s",
				name, fin, op, ui.Money(threshold), now)
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

const thresholdHelp = "under 40 / over 40 set the direction · a bare 40 infers it from the current price · enter accept · esc cancel"

func (m *Model) startWatchPick() tea.Cmd {
	m.detail = nil
	cmd := m.showView(viewHoldings)
	m.focus = paneCards
	m.filtering = true
	m.watchPick = true
	return cmd
}

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
