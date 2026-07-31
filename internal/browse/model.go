package browse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/arbitrage"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// pane identifies which side of the screen has the cursor.
type pane int

const (
	paneContainers pane = iota
	paneCards
)

// container is one row of the left pane: the loose collection, or a deck.
//
// The collection is not a deck and has no container row of its own in the
// listing queries, so it is synthesised here with ID 0. Kind is what the card
// pane branches on when it loads.
type container struct {
	ID     int64
	Name   string
	Kind   string
	Copies int
	Value  float64

	// meta is what a deck needs to be recreated after it is removed. Carried on
	// the row rather than re-read at deletion time because by then the container
	// is gone and there is nothing left to read it from.
	meta store.DeckMeta
}

// card is one row of the right pane, flattened from either source so the pane
// renders one way regardless of which side of the hoard it is showing.
//
// Board is empty for loose holdings, which is the honest representation: the
// collection has no boards, and printing "main" against every loose card would
// invent a distinction that is not there.
type card struct {
	ScryfallID      string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Board           string
	Quantity        int
	Price           *float64
	Value           float64
	AltSource       string
}

// pendingRemoval is a delete waiting on confirmation.
type pendingRemoval struct {
	prompt string
	deck   bool // a deck, rather than a card in the collection
}

// Model is the browser's state. Exported so tests can drive Update directly,
// the way internal/tui's tests drive the add cascade.
type Model struct {
	store Store

	width, height int
	ready         bool

	focus pane

	// Per-view sort state: which of sortColumns[view] orders the rows, and
	// whether that column runs backwards. Indexed by viewMode, so each view
	// keeps its own order across "v" cycles. Containers are always ranked by
	// value: the left pane is the summary, and a summary sorted by name would
	// stop answering the question it exists for.
	sortIdx [len(sortColumns)]int
	sortRev [len(sortColumns)]bool

	containers []container

	// cards is what the pane shows; allCards is what the container holds. The
	// unfiltered set is kept so narrowing and widening a filter as it is typed
	// costs no database reads.
	allCards []card
	cards    []card

	// Filter state. filtering is true while the bar is open and taking
	// keystrokes; the filter stays applied once it closes, so the bar is a mode
	// for editing the query rather than for having one.
	filtering  bool
	filterText string
	filter     filter
	// emptyNote explains an empty filtered pane; see refreshEmptyNote.
	emptyNote string
	filterErr string
	// allowed is the id set the trait half of the filter matched, or nil when
	// the filter asks nothing of the catalog.
	allowed map[string]bool

	// Cursors are kept per pane so switching back to the container list does not
	// lose your place in it, and a scroll offset per pane so a long list can
	// move under a fixed viewport.
	cursor [2]int
	offset [2]int

	// confirm is the pending destructive action, or nil. Removals ask first:
	// a single keystroke that deletes a hundred-card deck with no way back
	// through the same key that moves the cursor is a trap.
	confirm *pendingRemoval

	// undoStack holds the single reversible action. See undoAction.
	undoStack *undoAction

	// view is what the right pane is showing: the selected container's
	// holdings, or a hoard-wide analysis.
	view     viewMode
	movers   []store.PriceChange
	unpriced []store.UnpricedRow

	// valueSeries backs the holdings header's sparkline: the whole hoard's
	// worth over time, loaded once and on reload like everything else.
	valueSeries []store.ValuePoint

	// Arbitrage is the one view that needs the network, so unlike the others it
	// is fetched on request, asynchronously, and can be abandoned part-way.
	arbitrage  ArbitrageFunc
	arbResult  arbitrage.Result
	arbRows    []arbitrage.Row
	arbLoading bool
	arbLoaded  bool
	arbGen     int
	arbCancel  context.CancelFunc
	spinner    spinner.Model

	// ctx bounds every background fetch, so quitting the program stops them
	// rather than leaving a download running against a closed database.
	ctx context.Context

	// clock is injectable so a test can pin the movers window instead of
	// depending on when it runs.
	clock func() time.Time

	// detail is the open card overlay, or nil. Loaded once when opened rather
	// than re-read per frame, so scrolling the list behind it costs nothing.
	detail *detail

	// wantAdd is set when the user asked for the add cascade, so Run can tell
	// its caller to hand the terminal over and come back.
	wantAdd bool

	status    string
	statusErr bool

	// err is the failure that ended the session, surfaced by Run. A read that
	// fails mid-session becomes a status line instead: the screen already has
	// content worth keeping, and quitting would throw it away.
	err error
}

// New builds a model with the container list already loaded, so the first frame
// has content rather than a spinner over a database read that takes a
// millisecond.
func New(st Store, opts ...Option) (Model, error) {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	m := Model{store: st, focus: paneContainers, spinner: sp, ctx: context.Background()}
	for _, opt := range opts {
		opt(&m)
	}
	if err := m.loadContainers(); err != nil {
		return Model{}, err
	}
	if err := m.loadCards(); err != nil {
		return Model{}, err
	}
	if err := m.loadValueSeries(); err != nil {
		return Model{}, err
	}
	return m, nil
}

// loadValueSeries reads the hoard-value history behind the header sparkline.
func (m *Model) loadValueSeries() error {
	series, err := m.store.ValueSnapshots()
	if err != nil {
		return fmt.Errorf("reading value snapshots: %w", err)
	}
	m.valueSeries = series
	return nil
}

// pricePoints adapts a price series to the resampler's shape.
func pricePoints(s []store.PricePoint) []ui.TimePoint {
	out := make([]ui.TimePoint, len(s))
	for i, p := range s {
		out[i] = ui.TimePoint{AsOf: p.AsOf, Value: p.Price}
	}
	return out
}

// loadContainers reads the left pane: the binders (default first), then decks
// by value.
func (m *Model) loadContainers() error {
	binders, err := m.store.ListBinders()
	if err != nil {
		return fmt.Errorf("reading binders: %w", err)
	}
	decks, err := m.store.ListDecks()
	if err != nil {
		return fmt.Errorf("reading decks: %w", err)
	}

	out := make([]container, 0, len(binders)+len(decks))
	for _, b := range binders {
		out = append(out, container{
			ID: b.ID, Name: b.Name, Kind: store.KindCollection,
			Copies: b.TotalCopies, Value: b.Value,
		})
	}
	for _, d := range store.DecksByValue(decks) {
		out = append(out, container{
			ID: d.ID, Name: d.Name, Kind: store.KindDeck,
			Copies: d.TotalCopies, Value: d.Value,
			meta: store.DeckMeta{
				Name: d.Name, Source: d.Source, SourceID: d.SourceID,
				SourceURL: d.SourceURL, Format: d.Format,
			},
		})
	}
	m.containers = out
	m.clampCursor(paneContainers)
	return nil
}

// loadCards reads the right pane for whichever container is selected.
func (m *Model) loadCards() error {
	sel := m.selectedContainer()
	if sel == nil {
		m.cards = nil
		return nil
	}

	var out []card
	if sel.Kind == store.KindCollection {
		rows, err := m.store.BinderByFinish(sel.ID)
		if err != nil {
			return fmt.Errorf("reading binder %q: %w", sel.Name, err)
		}
		for _, r := range store.CollectionByValue(rows) {
			out = append(out, card{
				ScryfallID: r.ScryfallID, Name: r.Name, SetCode: r.SetCode,
				CollectorNumber: r.CollectorNumber, Finish: r.Finish,
				Quantity: r.Quantity, Price: r.Price(), Value: r.Value,
				AltSource: r.AltSource,
			})
		}
	} else {
		entries, err := m.store.DeckEntries(sel.ID)
		if err != nil {
			return fmt.Errorf("reading deck %q: %w", sel.Name, err)
		}
		for _, e := range store.EntriesByValue(entries) {
			out = append(out, card{
				ScryfallID: e.Card.ScryfallID, Name: e.Card.Name, SetCode: e.Card.SetCode,
				CollectorNumber: e.Card.CollectorNumber, Finish: e.Finish, Board: e.Board,
				Quantity: e.Quantity, Price: e.Price(), Value: e.Value(),
				AltSource: e.Card.AltSource,
			})
		}
	}

	m.allCards = out
	m.sortHoldings()
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
	return nil
}

// applyFilter narrows allCards into cards. It does not touch the database: the
// trait half was resolved when the query last changed, and holding terms are
// answered by the rows themselves.
func (m *Model) applyFilter() {
	if m.filter.empty() {
		m.cards = m.allCards
		m.refreshEmptyNote()
		return
	}
	out := make([]card, 0, len(m.allCards))
	for _, c := range m.allCards {
		if m.filter.matches(c, m.allowed) {
			out = append(out, c)
		}
	}
	m.cards = out
	m.clampCursor(paneCards)
	m.refreshEmptyNote()
}

// refreshEmptyNote explains an empty filtered pane, computed here — when the
// query or the rows change — rather than in the render path, which must not
// read the database (the invariant detail.go states for the whole package).
//
// Trait columns are NULL until update-prices has stored a Scryfall document, so
// on a hoard that has not been refreshed since the upgrade `rarity:mythic`
// correctly returns nothing — and looks exactly like a hoard containing no
// mythics. Only the count can tell those apart, and without saying so the
// feature reads as broken.
func (m *Model) refreshEmptyNote() {
	m.emptyNote = ""
	if len(m.cards) > 0 || m.filter.empty() || !m.filter.needsCatalog() {
		return
	}
	enriched, total, err := m.store.EnrichedCount()
	if err != nil || total == 0 {
		return
	}
	switch {
	case enriched == 0:
		m.emptyNote = "no card details stored yet — run hoard update-prices to filter by rarity, type or colour"
	case enriched < total:
		m.emptyNote = fmt.Sprintf("no matches · %d of %d cards have details; update-prices fills the rest",
			enriched, total)
	default:
		m.emptyNote = "no matches"
	}
}

// setFilter parses a query and resolves whatever part of it the catalog has to
// answer.
//
// Called on every keystroke while the bar is open, so the catalog query is
// skipped unless a trait term is actually present — which keeps a plain name
// search, the overwhelmingly common case, free of database work.
func (m *Model) setFilter(text string) {
	f, err := parseFilter(text)
	if err != nil {
		// The rows keep whatever the last valid query produced. Emptying the
		// pane on a half-typed `cmc>` would make the list flicker between every
		// keystroke of a comparison.
		m.filterErr = err.Error()
		return
	}
	m.filterErr = ""
	m.filter = f
	m.allowed = nil

	if f.needsCatalog() {
		ids, err := m.store.MatchingCardIDs(f.traits)
		if err != nil {
			m.setError(err)
			return
		}
		m.allowed = ids
	}
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
}

// clearFilter drops the query and restores the full pane.
func (m *Model) clearFilter() {
	m.filtering = false
	m.filterText = ""
	m.filterErr = ""
	m.filter = filter{}
	m.allowed = nil
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
}

// selectedContainer is the container under the left cursor, or nil when there
// are none.
func (m Model) selectedContainer() *container {
	if len(m.containers) == 0 {
		return nil
	}
	i := min(max(m.cursor[paneContainers], 0), len(m.containers)-1)
	return &m.containers[i]
}

// selectedCard is the card under the right cursor, or nil when the pane is
// empty — an empty deck, or a filter that matched nothing.
func (m Model) selectedCard() *card {
	if len(m.cards) == 0 {
		return nil
	}
	i := min(max(m.cursor[paneCards], 0), len(m.cards)-1)
	return &m.cards[i]
}

// rowCount is how many rows the focused pane holds.
func (m Model) rowCount(p pane) int {
	if p == paneContainers {
		return len(m.containers)
	}
	return m.viewRowCount()
}

// clampCursor keeps a cursor inside its pane after the rows underneath change.
func (m *Model) clampCursor(p pane) {
	n := m.rowCount(p)
	if n == 0 {
		m.cursor[p], m.offset[p] = 0, 0
		return
	}
	m.cursor[p] = min(max(m.cursor[p], 0), n-1)
}

// Init satisfies tea.Model. Everything is loaded before the first frame, so
// there is nothing to kick off.
func (m Model) Init() tea.Cmd { return nil }

// Update handles keys and resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.scrollIntoView()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case arbitrageMsg:
		return m.onArbitrage(msg)
	case spinner.TickMsg:
		// Only animate while something is actually in flight, or the program
		// wakes up forever after the fetch that needed it has finished.
		if !m.arbLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the bar is open it takes every printable key, or typing "q" in a
	// card name would quit.
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	// A pending removal takes precedence over everything but quitting, so the
	// prompt cannot be left hanging while the cursor wanders off the row it
	// refers to.
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	// The detail overlay covers the panes, so it takes the keys that would
	// otherwise move a cursor the reader cannot see.
	if m.detail != nil {
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "enter", "backspace":
			m.detail = nil
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "+", "=":
		// "=" is the unshifted key "+" lives on, so both work.
		m.adjustQuantity(1)
		return m, nil
	case "-", "_":
		m.adjustQuantity(-1)
		return m, nil

	case "a":
		// Quit with the flag set; Run's caller runs the cascade and re-enters.
		m.wantAdd = true
		return m, tea.Quit

	case "enter":
		if m.view == viewArbitrage {
			return m, m.startArbitrage()
		}
		if m.focus == paneCards {
			m.openDetail()
		} else {
			// Enter on a container is "show me this one", which means moving to
			// its cards — the same thing tab does, but where the hand already is.
			m.focus = paneCards
		}
		return m, nil

	case "d":
		m.askRemoval()
		return m, nil
	case "u":
		m.undoRecorded()
		return m, nil

	case "/":
		m.filtering = true
		m.focus = paneCards
		m.status = ""
		return m, nil
	case "esc":
		if m.arbLoading {
			m.cancelArbitrage()
			m.arbLoading = false
			m.status, m.statusErr = "cancelled", false
			return m, nil
		}
		if !m.filter.empty() {
			m.clearFilter()
			m.status = "filter cleared"
		}
		return m, nil

	case "tab":
		// Tab is what people press without thinking in a two-pane layout, so it
		// toggles rather than only ever moving one way.
		if m.focus == paneContainers {
			m.focus = paneCards
		} else {
			m.focus = paneContainers
		}
		m.status = ""
		return m, nil
	case "left", "h":
		m.focus = paneContainers
		m.status = ""
		return m, nil
	case "right", "l":
		m.focus = paneCards
		m.status = ""
		return m, nil

	case "up", "k":
		m.move(-1)
		return m, nil
	case "down", "j":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.visibleRows())
		return m, nil
	case "pgdown":
		m.move(m.visibleRows())
		return m, nil
	case "home", "g":
		m.cursor[m.focus] = 0
		m.scrollIntoView()
		return m, nil
	case "end", "G":
		m.cursor[m.focus] = max(m.rowCount(m.focus)-1, 0)
		m.scrollIntoView()
		return m, nil

	case "v":
		// Leaving arbitrage abandons any fetch still running: the answer is no
		// longer wanted, and a download that outlives the view is a surprise.
		m.cancelArbitrage()
		m.arbLoading = false
		m.view = m.view.next()
		m.focus = paneCards
		if err := m.loadView(); err != nil {
			m.setError(err)
			return m, nil
		}
		m.cursor[paneCards], m.offset[paneCards] = 0, 0
		m.status = "showing " + m.view.String()
		m.statusErr = false
		return m, nil

	case "s":
		m.cycleSort()
		m.cursor[paneCards], m.offset[paneCards] = 0, 0
		m.status, m.statusErr = "sorted by "+m.sortLabel(), false
		return m, nil
	case "S":
		m.reverseSort()
		m.cursor[paneCards], m.offset[paneCards] = 0, 0
		m.status, m.statusErr = "sorted by "+m.sortLabel(), false
		return m, nil

	case "r":
		m.reload()
		return m, nil
	}
	return m, nil
}

// askRemoval stages a delete for confirmation. Which pane has focus decides
// what is being removed: a deck on the left, a card on the right.
func (m *Model) askRemoval() {
	if m.focus == paneContainers {
		sel := m.selectedContainer()
		if sel == nil {
			return
		}
		if sel.Kind == store.KindCollection {
			m.status, m.statusErr = "the "+strings.ToLower(store.LooseName)+" cannot be removed", true
			return
		}
		m.confirm = &pendingRemoval{
			deck: true,
			prompt: fmt.Sprintf("remove deck %q and its %s cards?",
				sel.Name, ui.Count(sel.Copies)),
		}
		return
	}

	if ok, why := m.editable(); !ok {
		if why != "" {
			m.status, m.statusErr = why, true
		}
		return
	}
	c := m.selectedCard()
	if c == nil {
		return
	}
	m.confirm = &pendingRemoval{
		prompt: fmt.Sprintf("remove %s from the %s?", c.Name, strings.ToLower(store.LooseName)),
	}
}

// handleConfirmKey answers a pending removal. Only an explicit y proceeds:
// anything else, including enter, cancels — the safe reading of a stray
// keystroke is "no".
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	deck := m.confirm.deck
	m.confirm = nil

	if msg.String() != "y" {
		m.status, m.statusErr = "cancelled", false
		return m, nil
	}
	if deck {
		m.removeDeck()
	} else {
		m.removeCard()
	}
	return m, nil
}

// handleFilterKey edits the query while the bar is open.
//
// The pane narrows on every keystroke rather than on enter, so the query is
// judged by what it selects instead of by whether it reads right — which is the
// only way to discover that `type:creature` finds nothing because the catalog
// was never refreshed.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		// Escape abandons the whole query; enter keeps it and closes the bar.
		m.clearFilter()
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		if m.filter.empty() {
			m.filterText = ""
		}
		return m, nil
	case tea.KeyBackspace:
		if m.filterText != "" {
			r := []rune(m.filterText)
			m.filterText = string(r[:len(r)-1])
			m.setFilter(m.filterText)
		}
		return m, nil
	case tea.KeyCtrlU:
		m.filterText = ""
		m.setFilter("")
		return m, nil
	case tea.KeySpace:
		m.filterText += " "
		m.setFilter(m.filterText)
		return m, nil
	case tea.KeyRunes:
		m.filterText += string(msg.Runes)
		m.setFilter(m.filterText)
		return m, nil
	// Moving the cursor with the bar still open is worth allowing: you narrow
	// to a handful of cards and want to look at one without closing anything.
	case tea.KeyUp:
		m.move(-1)
		return m, nil
	case tea.KeyDown:
		m.move(1)
		return m, nil
	}
	return m, nil
}

// move walks the focused pane's cursor, clamping at both ends rather than
// wrapping: a list that jumps from the last row to the first loses your place
// on a long collection.
func (m *Model) move(delta int) {
	n := m.rowCount(m.focus)
	if n == 0 {
		return
	}
	m.cursor[m.focus] = min(max(m.cursor[m.focus]+delta, 0), n-1)
	m.scrollIntoView()

	// Moving in the container pane changes what the card pane is showing.
	if m.focus == paneContainers {
		if err := m.loadCards(); err != nil {
			m.setError(err)
		}
	}
}

// reload re-reads both panes, keeping the cursor where it was. This is what
// makes an edit made elsewhere — or an update-prices in another terminal —
// visible without restarting.
func (m *Model) reload() {
	if err := m.loadContainers(); err != nil {
		m.setError(err)
		return
	}
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	if err := m.loadValueSeries(); err != nil {
		m.setError(err)
		return
	}
	m.status = "reloaded"
	m.statusErr = false
}

// setError puts a failure on the status line rather than ending the session.
// The screen already holds content worth keeping.
func (m *Model) setError(err error) {
	m.status = err.Error()
	m.statusErr = true
}
