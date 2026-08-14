package browse

type place struct {
	cards, containers      int
	cardOffset, contOffset int
	cardsPage              int

	key string
}

type rereadScope int

const (
	rereadAll rereadScope = iota

	rereadLive
)

func (m Model) cardKey(c card) string {
	if sel := m.selectedContainer(); sel != nil && sel.Kind == kindAllCards {
		return c.Name + "|" + c.Finish.String()
	}
	return c.ScryfallID + "|" + c.Finish.String() + "|" + c.Condition + "|" + c.Board
}

func (m *Model) capturePlace() place {
	p := place{
		cards: m.cursor[paneCards], containers: m.cursor[paneContainers],
		cardOffset: m.offset[paneCards], contOffset: m.offset[paneContainers],
		cardsPage: m.cardsPage,
	}
	if m.view == viewHoldings {
		if c := m.selectedCard(); c != nil {
			p.key = m.cardKey(*c)
		}
	}
	return p
}

func (m *Model) seekCard(key string) bool {
	for i, c := range m.filteredCards {
		if m.cardKey(c) != key {
			continue
		}
		m.cardsPage = i / singleTablePageSize
		m.deriveCardsPage()
		m.cursor[paneCards] = i - m.cardsPage*singleTablePageSize
		return true
	}
	return false
}

func (m *Model) reread(scope rereadScope) error {

	m.dataGen++
	p := m.capturePlace()

	if err := m.loadContainers(); err != nil {
		return err
	}
	if err := m.rebuildEntryIndex(); err != nil {
		return err
	}
	m.cursor[paneContainers], m.offset[paneContainers] = p.containers, p.contOffset
	m.clampCursor(paneContainers)

	if err := m.loadCards(); err != nil {
		return err
	}

	m.cardsPage = p.cardsPage
	m.deriveCardsPage()
	m.cursor[paneCards], m.offset[paneCards] = p.cards, p.cardOffset
	m.clampCursor(paneCards)

	m.rowGone = false
	if scope == rereadLive && p.key != "" {
		m.rowGone = !m.seekCard(p.key)
	}

	if scope == rereadAll {

		if err := m.loadView(); err != nil {
			return err
		}
	}

	m.scrollIntoView()
	return nil
}

func (m *Model) refresh() {
	if err := m.reread(rereadAll); err != nil {
		m.setError(err)
	}
}

func (m *Model) reload() {
	if err := m.reread(rereadAll); err != nil {
		m.setError(err)
		return
	}
	m.status = "reloaded"
	m.statusErr = false
}
