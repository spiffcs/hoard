package browse

import (
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func (m *Model) loadSetContainers() error {
	sets, err := m.store.SetsHeld()
	if err != nil {
		return fmt.Errorf("reading sets: %w", err)
	}
	out := make([]container, 0, len(sets)+1)
	out = append(out, container{ID: allCardsID, Name: allCardsName, Kind: kindAllCards})
	m.containerNameW = ansi.StringWidth(allCardsName)
	for i, s := range sets {
		m.containerNameW = max(m.containerNameW, ansi.StringWidth(s.Name))
		out = append(out, container{

			ID:   allCardsID - 1 - int64(i),
			Name: s.Name, Kind: kindSet, setCode: s.Code,
			releasedAt: s.ReleasedAt,
			Copies:     s.Copies, Value: s.Value,
		})
		out[0].Copies += s.Copies
		out[0].Value += s.Value
	}
	m.containers = out
	m.clampCursor(paneContainers)
	return nil
}

const settlingMark = "*"

const setSettlingDays = "movers.settlingDays"

func (m *Model) loadSettlingWindow() {

	if _, pinned := store.SettlingDaysEnvAsk(); pinned {
		return
	}
	s, err := m.store.Settings()
	if err != nil {
		return
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s[setSettlingDays])); err == nil && n >= 0 {
		store.SetSettlingDays(n)
	}
}

func (m *Model) promptSetSettlingWindow() {
	m.prompt = &prompt{
		label:    "hold new sets out of the net for",
		text:     strconv.Itoa(store.SettlingDays()),
		help:     "days since release, like 90 (0 counts every set) · enter accept · esc cancel",
		validate: func(text string) error { _, err := parseSettlingDays(text); return err },
		commit: func(m *Model, text string) tea.Cmd {
			n, err := parseSettlingDays(text)
			if err != nil {
				m.status, m.statusErr = err.Error(), true
				return nil
			}
			store.SetSettlingDays(n)
			m.deriveView()
			switch env, pinned := store.SettlingDaysEnvAsk(); {

			case pinned:
				m.status, m.statusErr = fmt.Sprintf("settling window %s · %s=%d wins at next launch",
					ui.Plural(n, "day", "days"), store.SettlingDaysEnv, env), false
			case n == 0:
				m.status, m.statusErr = "settling window off · every set counts", false
			default:
				m.status, m.statusErr = fmt.Sprintf("settling window %s · new sets held out of the movers net",
					ui.Plural(n, "day", "days")), false
			}
			if err := m.store.SaveSettings(map[string]string{setSettlingDays: strconv.Itoa(n)}); err != nil {
				m.status, m.statusErr = "saving settling window: "+err.Error(), true
			}
			return nil
		},
	}
}

func parseSettlingDays(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("give a number of days, or 0 to count every set")
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number of days", text)
	}
	if n < 0 {
		return 0, fmt.Errorf("a window cannot be negative; 0 counts every set")
	}
	return n, nil
}

func (c container) settling(now time.Time) bool {
	return c.Kind == kindSet && store.Settling(c.releasedAt, now)
}

func (m Model) anySettling(now time.Time) bool {
	for _, c := range m.containers {
		if c.settling(now) {
			return true
		}
	}
	return false
}

func (m *Model) toggleSetsMode() {
	m.setsMode = !m.setsMode
	m.cursor[paneContainers], m.offset[paneContainers] = 0, 0
	m.displacedContainer = 0
	if err := m.loadContainers(); err != nil {

		m.setsMode = !m.setsMode
		m.setError(err)
		return
	}
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	m.deriveView()
	if m.setsMode {
		m.status, m.statusErr = "browsing by set · newest first · B returns to binders and decks", false
	} else {
		m.status, m.statusErr = "browsing binders and decks · B returns to sets", false
	}
}

func (m *Model) setRowHoldings(c *card) (binders []store.Holding, refusal string, errored bool) {
	holdings, err := m.store.HoldingsOf(c.ScryfallID)
	if err != nil {
		m.setError(err)
		return nil, "", true
	}
	deckCopies := false
	for _, h := range holdings {
		if h.Finish != c.Finish {
			continue
		}
		if h.ContainerKind != store.KindCollection {
			deckCopies = true
			continue
		}

		if h.Board != "" && h.Board != "main" {
			continue
		}
		binders = append(binders, h)
	}
	switch {
	case len(binders) > 0:
		return binders, "", false
	case deckCopies:
		return nil, "every copy of " + c.Name + " is in a deck · enter opens the card to edit it there", false
	default:
		return nil, "no binder holds " + c.Name + " (" + c.Finish.String() + ") · r reloads", false
	}
}

func (m *Model) adjustSetQuantity(delta int) {
	c := m.selectedCard()
	if c == nil {
		return
	}
	binders, refusal, errored := m.setRowHoldings(c)
	if errored {
		return
	}
	if refusal != "" {
		m.status, m.statusErr = refusal, true
		return
	}
	if len(binders) > 1 {
		m.status, m.statusErr = fmt.Sprintf(
			"%s is in %d binders · enter opens the card to edit each copy", c.Name, len(binders)), true
		return
	}

	h := binders[0]

	want := max(h.Quantity+delta, 0)
	if want == h.Quantity {
		return
	}
	ref := store.EntryRef{ContainerID: h.ContainerID, ScryfallID: c.ScryfallID,
		Finish: c.Finish, Condition: h.Condition, Board: h.Board}
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
		m.status = fmt.Sprintf("removed %s (%s) from %s", name, finish, h.ContainerName)
	} else {
		m.status = fmt.Sprintf("%s (%s) ×%d in %s", name, finish, want, h.ContainerName)
	}
	m.statusErr = false
	m.refresh()
}

func (m *Model) askSetRemoval() {
	c := m.selectedCard()
	if c == nil {
		return
	}
	binders, refusal, errored := m.setRowHoldings(c)
	if errored {
		return
	}
	if refusal != "" {
		m.status, m.statusErr = refusal, true
		return
	}
	name, id, finish := c.Name, c.ScryfallID, c.Finish
	prompt := fmt.Sprintf("remove %s (%s) from %d binders?", name, finish, len(binders))
	if len(binders) == 1 {
		prompt = fmt.Sprintf("remove %s (%s) ×%d from %s?",
			name, finish, binders[0].Quantity, binders[0].ContainerName)
	}
	m.confirm = &pendingConfirm{
		prompt: prompt,
		help:   "y remove · any other key cancels",
		onYes:  func(m *Model) tea.Cmd { m.removeSetRow(name, id, finish, binders); return nil },
	}
}

func (m *Model) removeSetRow(name, id string, fin finish.Finish, binders []store.Holding) {
	var removed []store.Holding
	record := func() {
		if len(removed) == 0 {
			return
		}
		gone := removed
		m.undoable(undoAction{
			desc: name,
			undo: func(st Editor) error { return st.RestoreHoldings(id, gone) },
		})
	}
	for _, h := range binders {
		previous, err := m.store.SetEntryQuantity(store.EntryRef{ContainerID: h.ContainerID,
			ScryfallID: id, Finish: fin, Condition: h.Condition, Board: h.Board}, 0)
		if err != nil {

			record()
			m.setError(err)
			return
		}

		removed = append(removed, store.Holding{
			ContainerID: h.ContainerID, ContainerName: h.ContainerName,
			ContainerKind: store.KindCollection, Finish: fin,
			Condition: h.Condition, Board: store.BoardMain, Quantity: previous,
		})
	}
	record()
	if len(binders) == 1 {
		m.status = fmt.Sprintf("removed %s (%s) from %s", name, fin, binders[0].ContainerName)
	} else {
		m.status = fmt.Sprintf("removed %s (%s) from %d binders", name, fin, len(binders))
	}
	m.statusErr = false
	m.refresh()
}
