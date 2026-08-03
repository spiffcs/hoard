package browse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

// pane identifies which side of the screen has the cursor.
type pane int

const (
	paneContainers pane = iota
	paneCards
)

// allCardsID and kindAllCards mark the synthetic top row of the left pane:
// every holding in the hoard, all binders and all decks merged into one
// list. Binders and decks below it are subsets. The row is browse-only —
// no store container backs it, hence the sentinel id.
const allCardsID int64 = -1
const kindAllCards = "all"

// allCardsName is the merged row's on-screen name.
const allCardsName = "All cards"

// container is one row of the left pane: the merged all-cards view, the
// loose collection, or a deck. Kind is what the card pane branches on when
// it loads.
type container struct {
	ID     int64
	Name   string
	Kind   string
	Copies int
	Value  float64
	// isDefault marks the built-in binder, which cannot be renamed or
	// removed; carried from the store rather than inferred from position.
	isDefault bool

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
	// ColorIdentity is the WUBRG identity, nil when unknown — same
	// semantics as store.Card.ColorIdentity.
	ColorIdentity []string
}

// pendingConfirm is a staged action waiting on confirmation. Only an
// explicit y runs onYes; anything else, including enter, cancels — the safe
// reading of a stray keystroke is "no". Removals were the first users; any
// destructive or expensive action stages the same way. onNo (optional) runs
// on every non-yes resolution including ctrl+c — it exists for the confirm
// bridge, where a blocked worker must hear "no" or hang forever. help is
// the help-line wording; empty gets a generic confirm line.
type pendingConfirm struct {
	prompt string
	help   string
	onYes  func(*Model) tea.Cmd
	onNo   func(*Model)
}

// Model is the browser's state. Exported so tests can drive Update directly,
// the way internal/tui's tests drive the add cascade.
type Model struct {
	store Store

	// env is the terminal the browser renders for — detected from the real
	// stdout by New, pinned by WithEnv in tests. Its Color field is what
	// makes browse honor NO_COLOR; width still arrives per-frame from
	// WindowSizeMsg. theme is the shared ui palette; no styles are defined
	// in this package. imgTier is what DetectImageTier decided the terminal
	// can draw (the card-image spike); imageFetch is its injected fetch.
	env        ui.Env
	theme      ui.Theme
	imgTier    ui.ImageTier
	imageFetch CardImageFunc

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
	confirm *pendingConfirm

	// prompt is an inline one-line input when non-nil; see prompt.go.
	prompt *prompt

	// watchPick marks the pick-a-card-to-watch flow: the next enter on a
	// card runs the watch prompt instead of opening its detail.
	watchPick bool

	// floorIdx indexes floorLevels: the value floor hiding cards priced
	// under it, across every view that has prices. 0 is off.
	floorIdx int

	// commands is the registry, built once; palette is the open drawer over
	// it, nil when closed. See command.go and palette.go.
	commands []command
	palette  *palette

	// The injected long-running operations (nil = unavailable) and the one
	// in flight; see opstate.go.
	opUpdatePrices   OpFunc
	opRepairFinishes OpFunc
	opCatalogUpdate  OpFunc
	catalogOffer     bool
	opBackfill       BackfillFunc
	opWatchAdd       WatchAddFunc
	opDeckAdd        DeckAddFunc
	opImport         ImportFunc
	op               *opState
	opGen            int

	// undoStack holds the single reversible action. See undoAction.
	undoStack *undoAction

	// view is what the right pane is showing: the selected container's
	// holdings, or an analysis filtered to it.
	view     viewMode
	movers   []store.PriceChange
	unpriced []store.UnpricedRow
	watches  []store.WatchStatus

	// The pristine analytical rows as queried; the floor and the container
	// filter derive the visible slices above from these (deriveView), so
	// cycling either never re-reads the database.
	allMovers   []store.PriceChange
	allUnpriced []store.UnpricedRow
	allWatches  []store.WatchStatus

	// moversCache keeps each lookback window's pristine movers for the
	// session: the query walks the whole price history twice, and paying
	// that on every W press made the key feel broken (observed live).
	// dataGen invalidates it — bumped wherever prices or holdings change.
	moversCache    map[int][]store.PriceChange
	moversCacheGen int
	dataGen        int

	// moversPennies shows the sub-$0.20 movers the view hides by default:
	// bulk wobbling by cents is volume without information. Separate from
	// the value floor — this is the movers view's own noise gate, toggled
	// from the palette.
	moversPennies bool

	// entryIndex answers "does this container hold this printing":
	// containerID → "scryfallID|finish". viewEligible marks the containers
	// selectable on views that grey the rest out, nil when all are. Both
	// live in containerfilter.go.
	entryIndex   map[int64]map[string]bool
	viewEligible map[int64]bool

	// The market view sorts per table: each of the three sections keeps
	// its own column choice and direction, indexed by market.Kind, and
	// 's' operates on the table the cursor is in. The comps section has
	// no Kind, so it keeps its own slice and sort state (see comps.go).
	marketSortIdx [3]int
	marketSortRev [3]bool
	marketComps   []market.Comp
	compsSortIdx  int
	compsSortRev  bool

	// marketSecOffset is each market section's scroll position inside its
	// fixed region (0=profit, 1=liquid, 2=comps); the tables scroll
	// independently rather than as one document.
	marketSecOffset [3]int

	// compsBuySide flips the COMPS table from its default sell side (what
	// a sale brings: anchor, bid, ratio) to the buy side (every vendor's
	// ask). 'b' toggles; selling is the default question.
	compsBuySide bool

	// moversDaysIdx indexes moversWindowDays; 'W' cycles it.
	moversDaysIdx int

	// Arbitrage is the one view that needs the network, so unlike the others it
	// is fetched on request, asynchronously, and can be abandoned part-way.
	// cardComps serves the detail overlay's comp sheets from the same
	// day cache, nil when the capability is absent.
	marketFetch   MarketFunc
	marketCached  MarketCachedFunc
	cardComps     CardCompFunc
	openURL       OpenURLFunc
	marketResult  market.Result
	marketRows    []market.Row
	marketLoading bool
	marketLoaded  bool
	marketGen     int
	marketCancel  context.CancelFunc
	spinner       spinner.Model

	// ctx bounds every background fetch, so quitting the program stops them
	// rather than leaving a download running against a closed database.
	ctx context.Context

	// clock is injectable so a test can pin the movers window instead of
	// depending on when it runs.
	clock func() time.Time

	// detail is the open card overlay, or nil. Loaded once when opened rather
	// than re-read per frame, so scrolling the list behind it costs nothing.
	detail *detail

	// text is the open scrollable text takeover (report, import outcome),
	// or nil. See textview.go.
	text *textView

	// reportFn produces the valuation report as renderable lines; injected
	// (nil = unavailable) so browse stays free of the action layer.
	reportFn ReportFunc

	// exportFn writes holdings to disk; injected for the same reason.
	exportFn ExportFunc

	// The confirm bridge (opconfirm.go): confirmCh is where op goroutines
	// ask their questions; deferredAsk parks the one request that can
	// arrive while a user-staged confirm is already up.
	confirmCh   <-chan ConfirmRequest
	deferredAsk *ConfirmRequest

	// The embedded add cascade: newAddChild is the injected constructor
	// (nil = capability absent), addChild the live takeover or nil, and
	// addSummary the session's accumulated receipt across every cascade
	// invocation, printed to scrollback when the browser exits so the
	// record of unattended writes outlives the alt screen.
	newAddChild func() (tui.Child, error)
	addChild    *tui.Child
	addSummary  tui.Summary

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
	m := Model{store: st, focus: paneContainers, spinner: sp, ctx: context.Background(),
		env: ui.Detect(os.Stdout), theme: ui.DefaultTheme(), imgTier: ui.DetectImageTier(),
		commands: commands()}
	for _, opt := range opts {
		opt(&m)
	}
	if err := m.loadContainers(); err != nil {
		return Model{}, err
	}
	if err := m.rebuildEntryIndex(); err != nil {
		return Model{}, err
	}
	if err := m.loadCards(); err != nil {
		return Model{}, err
	}
	m.showFiredBanner()
	return m, nil
}

// catalogFirstRunMsg fires once from Init when the browser opened with no
// catalog built, and starts the download as an ordinary operation.
type catalogFirstRunMsg struct{}

// showFiredBanner previews the watches whose thresholds hold unacknowledged
// — read-only, never consuming the alert (cron's `hoard watch` stays the
// consumer of record), which is why it repeats on every open until a real
// check runs. It is a transient status: the first status-clearing key
// dismisses it.
func (m *Model) showFiredBanner() {
	fired, err := m.store.WouldFire()
	if err != nil || len(fired) == 0 {
		return
	}
	switch {
	case len(fired) == 1:
		w := fired[0]
		m.status = fmt.Sprintf("1 watch met its threshold: %s %s %s · v to view",
			w.Name, w.Op, ui.Money(w.Threshold))
	case len(fired) == 2:
		m.status = fmt.Sprintf("2 watches met their threshold: %s %s %s, %s %s %s · v to view",
			fired[0].Name, fired[0].Op, ui.Money(fired[0].Threshold),
			fired[1].Name, fired[1].Op, ui.Money(fired[1].Threshold))
	default:
		m.status = fmt.Sprintf("%d watches met their threshold · v to view", len(fired))
	}
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

	out := make([]container, 0, len(binders)+len(decks)+1)
	// The merged view leads: it is the hoard itself, and everything below
	// it is a subset.
	all := container{ID: allCardsID, Name: allCardsName, Kind: kindAllCards}
	out = append(out, all)
	for _, b := range binders {
		out = append(out, container{
			ID: b.ID, Name: b.Name, Kind: store.KindCollection,
			Copies: b.TotalCopies, Value: b.Value, isDefault: b.IsDefault,
		})
		out[0].Copies += b.TotalCopies
		out[0].Value += b.Value
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
		out[0].Copies += d.TotalCopies
		out[0].Value += d.Value
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
	if sel.Kind == kindAllCards || sel.Kind == store.KindCollection {
		var rows []store.CollectionRow
		var err error
		if sel.Kind == kindAllCards {
			rows, err = m.store.AllByFinish()
		} else {
			rows, err = m.store.BinderByFinish(sel.ID)
		}
		if err != nil {
			return fmt.Errorf("reading %q: %w", sel.Name, err)
		}
		for _, r := range store.CollectionByValue(rows) {
			out = append(out, card{
				ScryfallID: r.ScryfallID, Name: r.Name, SetCode: r.SetCode,
				CollectorNumber: r.CollectorNumber, Finish: r.Finish,
				Quantity: r.Quantity, Price: r.Price(), Value: r.Value,
				AltSource: r.AltSource, ColorIdentity: r.ColorIdentity,
			})
		}
		if sel.Kind == kindAllCards {
			// The hoard-level list answers "how many of this card", so
			// same-name printings collapse into one row — ten Forests
			// across four printings were ten rows each wearing a set the
			// pile as a whole doesn't have (observed live). The detail's
			// held list is where the exact printings live.
			out = mergeByName(out)
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
				AltSource: e.Card.AltSource, ColorIdentity: e.Card.ColorIdentity,
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

// mergeByName collapses same-name same-finish rows into one. Quantities
// and values sum; the printing columns and the per-copy price survive only
// when every merged row agrees — a set naming one of three printings, or a
// price true of some copies, is worse than a dash. The first row's
// scryfall id stays as the representative, which is all enter-detail
// needs: the detail loads holdings by name and shows every printing.
func mergeByName(rows []card) []card {
	idx := map[string]int{}
	var out []card
	for _, c := range rows {
		key := c.Name + "|" + c.Finish
		i, seen := idx[key]
		if !seen {
			idx[key] = len(out)
			out = append(out, c)
			continue
		}
		m := &out[i]
		m.Quantity += c.Quantity
		m.Value += c.Value
		if m.SetCode != c.SetCode || m.CollectorNumber != c.CollectorNumber {
			m.SetCode, m.CollectorNumber = "", ""
		}
		if m.Price == nil || c.Price == nil || *m.Price != *c.Price {
			m.Price = nil
		}
	}
	return out
}

// applyFilter narrows allCards into cards. It does not touch the database: the
// trait half was resolved when the query last changed, and holding terms are
// answered by the rows themselves.
func (m *Model) applyFilter() {
	if m.filter.empty() && m.floorMin() == 0 {
		m.cards = m.allCards
		m.refreshEmptyNote()
		return
	}
	out := make([]card, 0, len(m.allCards))
	for _, c := range m.allCards {
		if m.filter.matches(c, m.allowed) && !m.underFloor(c.Price) {
			out = append(out, c)
		}
	}
	m.cards = out
	m.clampCursor(paneCards)
	m.refreshEmptyNote()
}

// floorLevels are the value-floor presets M cycles through; 0 is off.
var floorLevels = []float64{0, 5, 10, 25}

// floorMin is the active mask threshold, 0 when off.
func (m Model) floorMin() float64 { return floorLevels[m.floorIdx] }

// underFloor reports whether a per-copy price falls under the floor. With
// a floor set, an unknown price is under it — the floor asks for cards
// worth at least this much, and "unknown" is not an answer.
func (m Model) underFloor(p *float64) bool {
	if min := m.floorMin(); min > 0 {
		return p == nil || *p < min
	}
	return false
}

// cycleFloor advances the value floor and re-derives the current view
// through it — from the pristine slices, so no database read.
func (m *Model) cycleFloor() {
	m.floorIdx = (m.floorIdx + 1) % len(floorLevels)
	m.applyFilter()
	m.deriveView()
	m.clampCursor(paneCards)
	if min := m.floorMin(); min > 0 {
		m.status, m.statusErr = fmt.Sprintf("floor %s · hiding cards worth less", ui.Money(min)), false
	} else {
		m.status, m.statusErr = "floor off", false
	}
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
		m.emptyNote = "no card details stored yet · press : and run Update prices to filter by rarity, type or colour"
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

// Init arms the confirm-bridge pump (nil without WithConfirm). Everything
// else is loaded before the first frame.
func (m Model) Init() tea.Cmd {
	init := awaitConfirm(m.ctx, m.confirmCh)
	// The first-run catalog download starts itself: its whole value is
	// fast lookups in the add flow, so it belongs before the first add —
	// visible in the ordinary op slot, cancellable like any operation.
	if m.catalogOffer && m.opCatalogUpdate != nil {
		return tea.Batch(init, func() tea.Msg { return catalogFirstRunMsg{} })
	}
	return init
}

// Update handles keys and resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.scrollIntoView()
		if m.addChild != nil {
			child, _ := m.addChild.Update(msg)
			m.addChild = &child
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case marketMsg:
		return m.onMarket(msg)
	case imageMsg:
		return m.onImage(msg)
	case catalogFirstRunMsg:
		return m, m.startOp("updating the catalog", m.opCatalogUpdate)
	case opProgressMsg:
		return m.onOpProgress(msg)
	case opDoneMsg:
		// Handled by the browser even while the cascade covers the panes —
		// this is what lets an op finish behind an add.
		return m.onOpDone(msg)
	case opConfirmMsg:
		return m.onOpConfirm(msg)
	case spinner.TickMsg:
		// Delivered to both models: bubbles tags ticks with the owning
		// spinner's ID and each Update rejects foreign ones, so the two
		// re-arm chains stay singular. Browse's side only animates while
		// something is in flight, or the program wakes forever.
		var cmds []tea.Cmd
		if m.marketLoading || m.op != nil {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.addChild != nil {
			child, cmd := m.addChild.Update(msg)
			m.addChild = &child
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	// Everything unnamed belongs to the cascade while it is up: its message
	// types are unexported, so this default forward is the only routing
	// possible — and the right one. After it closes, stragglers die here.
	if m.addChild != nil {
		return m.forwardToChild(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode() {
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modeAddChild:
		return m.handleAddChildKey(msg)
	case modePrompt:
		return m.handlePromptKey(msg)
	case modePalette:
		return m.handlePaletteKey(msg)
	case modeFilter:
		return m.handleFilterKey(msg)
	case modeDetail:
		return m.handleDetailKey(msg)
	case modeText:
		return m.handleTextKey(msg)
	}
	return m.handleBrowseKey(msg)
}

// handleDetailKey drives the card overlay: close, quit, walk the vendor
// links, open one.
func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		m.detail = nil
	case "enter":
		// Enter used to close (esc's job too); with links it opens the
		// selected vendor page instead — the reason to have a cursor.
		if m.openURL != nil && len(m.detail.links) > 0 {
			l := m.detail.links[m.detail.linkCursor]
			if err := m.openURL(l.url); err != nil {
				m.setError(err)
			}
			return m, nil
		}
		m.detail = nil
	case "up", "k":
		// The vertical axis walks the held list; a different printing
		// re-points the overlay and refetches its art.
		return m, m.moveHeldCursor(-1)
	case "down", "j":
		return m, m.moveHeldCursor(1)
	case "left", "h":
		if n := len(m.detail.links); n > 0 {
			m.detail.linkCursor = max(m.detail.linkCursor-1, 0)
		}
	case "right", "l":
		if n := len(m.detail.links); n > 0 {
			m.detail.linkCursor = min(m.detail.linkCursor+1, n-1)
		}
	case ":", "ctrl+p":
		// The palette opens over the overlay — narrowed to the price
		// refreshers (see detailPaletteIDs): running one must not cost
		// the reader their place, and everything else waits an esc away.
		m.openPalette()
	}
	return m, nil
}

// handleBrowseKey is the ordinary two-pane surface: navigation, edits, and
// everything the help line advertises.
func (m Model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Deliberate actions live in the registry; what remains below is
	// navigation and mode openers.
	if cmd, ok := m.runCommand(msg.String()); ok {
		return m, cmd
	}
	switch msg.String() {
	// ctrl+c quits immediately (with a confirm only when an op would be
	// stranded); q asks first — quit intent from a single printable key
	// deserves a y/n, not a dumped session.
	case "ctrl+c":
		if m.op != nil {
			m.stageQuit()
			return m, nil
		}
		return m, tea.Quit
	case "q":
		m.stageQuit()
		return m, nil

	case ":", "ctrl+p":
		m.openPalette()
		return m, nil

	case "+", "=":
		// "=" is the unshifted key "+" lives on, so both work.
		m.adjustQuantity(1)
		return m, nil
	case "-", "_":
		m.adjustQuantity(-1)
		return m, nil

	case "enter":
		if m.focus == paneCards {
			if m.watchPick {
				m.finishWatchPick()
				return m, nil
			}
			m.openDetail()
			return m, m.fetchDetailImage()
		} else {
			// Enter on a container is "show me this one", which means moving to
			// its cards — the same thing tab does, but where the hand already is.
			m.focus = paneCards
		}
		return m, nil

	case "/":
		m.filtering = true
		m.focus = paneCards
		m.status = ""
		return m, nil
	case "esc":
		if m.watchPick {
			// The pick began on the watches view; cancelling puts the
			// reader back there rather than stranding them on holdings.
			m.watchPick = false
			cmd := m.showView(viewWatches)
			m.status, m.statusErr = "watch cancelled", false
			return m, cmd
		}
		if m.marketLoading {
			m.cancelMarketFetch()
			m.marketLoading = false
			m.status, m.statusErr = "cancelled", false
			return m, nil
		}
		if !m.filter.empty() {
			m.clearFilter()
			m.status = "filter cleared"
			return m, nil
		}
		if m.op != nil {
			// Late in the chain, so the everyday esc reflexes above can
			// never eat a minutes-long operation by accident.
			m.cancelOp()
			return m, nil
		}
		// Nothing left to back out of: esc at the top frame asks about
		// leaving, so backing "up" one frame too many never dumps the
		// session without warning.
		m.stageQuit()
		return m, nil

	case "tab":
		// Tab is what people press without thinking in a two-pane layout, so it
		// toggles rather than only ever moving one way. The container pane
		// applies on every view: it filters the analysis to the selection.
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
		m.moveTo(0)
		return m, nil
	case "end", "G":
		m.moveTo(m.rowCount(m.focus) - 1)
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
		if sel.Kind == kindAllCards {
			m.status, m.statusErr = allCardsName+" is every container merged; remove its subsets instead", true
			return
		}
		if sel.Kind == store.KindCollection {
			if sel.isDefault {
				m.status, m.statusErr = "the "+strings.ToLower(store.LooseName)+" cannot be removed", true
				return
			}
			m.askBinderRemoval(sel)
			return
		}
		m.confirm = &pendingConfirm{
			help: "y remove · any other key cancels",
			prompt: fmt.Sprintf("remove deck %q and its %s cards?",
				sel.Name, ui.Count(sel.Copies)),
			onYes: func(m *Model) tea.Cmd { m.removeDeck(); return nil },
		}
		return
	}

	// On the watches view, 'd' removes the watch under the cursor.
	if w := m.selectedWatch(); w != nil {
		m.askWatchRemoval(*w)
		return
	}
	if m.view == viewWatches {
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
	m.confirm = &pendingConfirm{
		prompt: fmt.Sprintf("remove %s from the %s?", c.Name, strings.ToLower(store.LooseName)),
		help:   "y remove · any other key cancels",
		onYes:  func(m *Model) tea.Cmd { m.removeCard(); return nil },
	}
}

// handleConfirmKey answers a staged confirm; see pendingConfirm for the
// only-y-proceeds contract.
// stageQuit asks before leaving: the plain quit confirm, or — with an
// operation still running — the variant whose yes also cancels it so no
// goroutine writes into a dead program.
func (m *Model) stageQuit() {
	if m.op != nil {
		title := m.op.title
		m.confirm = &pendingConfirm{
			prompt: title + " is still running. Quit anyway?",
			help:   "y quit · any other key stays",
			onYes: func(m *Model) tea.Cmd {
				m.cancelOp()
				return tea.Quit
			},
		}
		return
	}
	m.confirm = &pendingConfirm{
		prompt: "quit hoard?",
		help:   "y quit · any other key stays",
		onYes:  func(*Model) tea.Cmd { return tea.Quit },
	}
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		// Hard exit, but nobody gets left hanging: a bridge worker blocked
		// on this question hears "no", and a running op is cancelled so it
		// unwinds before the store closes underneath it.
		if m.confirm.onNo != nil {
			m.confirm.onNo(&m)
		}
		m.cancelOp()
		return m, tea.Quit
	}
	c := m.confirm
	m.confirm = nil

	var cmd tea.Cmd
	if msg.String() != "y" {
		// The generic receipt first, so an onNo with something better to
		// say (the catalog offer's palette pointer) gets the last word.
		m.status, m.statusErr = "cancelled", false
		if c.onNo != nil {
			c.onNo(&m)
		}
	} else {
		cmd = c.onYes(&m)
	}
	// A question the bridge parked while this confirm was up comes next.
	if m.deferredAsk != nil {
		req := *m.deferredAsk
		m.deferredAsk = nil
		m.stageConfirmRequest(req)
	}
	return m, cmd
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
		// In a watch pick it abandons the pick with it, back to the watches
		// view the flow began on — the help line says "esc cancel", and a
		// cancel that leaves you somewhere new is not one.
		m.clearFilter()
		if m.watchPick {
			m.watchPick = false
			cmd := m.showView(viewWatches)
			m.status, m.statusErr = "watch cancelled", false
			return m, cmd
		}
		return m, nil
	case tea.KeyTab:
		// Tab keeps the query but crosses to the containers pane: picking a
		// watch target (or plain filtering) may need another binder's cards.
		m.filtering = false
		if m.filter.empty() {
			m.filterText = ""
		}
		m.focus = paneContainers
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		if m.filter.empty() {
			m.filterText = ""
		}
		// Picking a watch target: enter on the narrowed list is the pick
		// itself, not just closing the bar — filter, enter, done.
		if m.watchPick && m.focus == paneCards {
			m.finishWatchPick()
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
// on a long collection. On views that grey out empty containers the left
// cursor steps over them rather than resting on a row it cannot select.
func (m *Model) move(delta int) {
	n := m.rowCount(m.focus)
	if n == 0 {
		return
	}
	if m.focus == paneContainers && m.viewSkips() {
		m.cursor[paneContainers] = m.stepEligible(m.cursor[paneContainers], delta)
	} else {
		m.cursor[m.focus] = min(max(m.cursor[m.focus]+delta, 0), n-1)
	}
	m.onCursorMoved()
}

// moveTo jumps the focused cursor to target — the home/end keys. An
// ineligible target walks back toward All cards, which is always eligible.
func (m *Model) moveTo(target int) {
	n := m.rowCount(m.focus)
	if n == 0 {
		return
	}
	target = min(max(target, 0), n-1)
	if m.focus == paneContainers && m.viewSkips() {
		for target > 0 && !m.containerEligible(target) {
			target--
		}
	}
	m.cursor[m.focus] = target
	m.onCursorMoved()
}

// stepEligible walks up to |delta| eligible container rows in delta's
// direction, landing on the furthest one found; with none in that
// direction the cursor stays put.
func (m Model) stepEligible(from, delta int) int {
	if delta == 0 {
		return from
	}
	dir, steps := 1, delta
	if delta < 0 {
		dir, steps = -1, -delta
	}
	last := from
	for pos := from + dir; pos >= 0 && pos < len(m.containers) && steps > 0; pos += dir {
		if m.containerEligible(pos) {
			last = pos
			steps--
		}
	}
	return last
}

// onCursorMoved is what every cursor move ends with: the scroll follows,
// and a move in the container pane re-scopes both the card pane and the
// current view's rows.
func (m *Model) onCursorMoved() {
	m.scrollIntoView()
	if m.focus != paneContainers {
		return
	}
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	m.deriveView()
}

// reload re-reads both panes, keeping the cursor where it was. This is what
// makes an edit made elsewhere — or an update-prices in another terminal —
// visible without restarting.
func (m *Model) reload() {
	m.dataGen++
	if err := m.loadContainers(); err != nil {
		m.setError(err)
		return
	}
	if err := m.rebuildEntryIndex(); err != nil {
		m.setError(err)
		return
	}
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	// The analytical views re-read too: they now depend on the membership
	// this reload may have changed, and "reloaded" must not mean "except
	// the rows you are looking at".
	if err := m.loadView(); err != nil {
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
