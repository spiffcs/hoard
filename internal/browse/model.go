package browse

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardfilter"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

type pane int

const (
	paneContainers pane = iota
	paneCards
)

const allCardsID int64 = -1
const kindAllCards = "all"

const kindSet = "set"

const kindFolder = store.KindFolder

const allCardsName = "All Cards"

type container struct {
	ID     int64
	Name   string
	Kind   string
	Copies int
	Value  float64

	isDefault bool
	Counted   bool

	parentID int64
	depth    int

	setCode string

	releasedAt string

	meta store.DeckMeta
}

type card struct {
	ScryfallID      string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          finish.Finish

	Condition string
	Board     string
	Quantity  int
	Price     *float64
	Value     float64
	Paid      *float64
	AltSource string

	ColorIdentity []string

	Treatment string

	Where string

	HeldIn string

	ImageURI string
}

type pendingConfirm struct {
	prompt string
	help   string
	onYes  func(*Model) tea.Cmd
	onNo   func(*Model)
}

type Model struct {
	store Store

	env        ui.Env
	theme      ui.Theme
	imgTier    ui.ImageTier
	imageFetch CardImageFunc

	cellAspect float64

	width, height int
	ready         bool

	resizeGen int

	focus pane

	allDips          []store.TrendRow
	allMomentum      []store.TrendRow
	filteredDips     []store.TrendRow
	filteredMomentum []store.TrendRow
	dips             []store.TrendRow
	momentum         []store.TrendRow

	dipPage      [2]int
	dipSecOffset [2]int
	dipSortIdx   [dipSectionCount]int
	dipSortRev   [dipSectionCount]bool

	selAnchor int

	sortIdx [len(sortColumns)]int
	sortRev [len(sortColumns)]bool

	containers []container

	setsMode bool

	allCards      []card
	filteredCards []card
	cards         []card
	cardsPage     int

	cardsColW  cardColWidths
	moversColW moverColWidths

	filtering  bool
	filterText string
	filter     cardfilter.Filter

	emptyNote string
	filterErr string

	allowed map[string]bool

	cursor [2]int
	offset [2]int

	confirm *pendingConfirm

	prompt *prompt

	watchPick bool

	floorIdx int

	commands []command
	palette  *palette

	opUpdatePrices   OpFunc
	opCorrectPrices  OpFunc
	opRepairFinishes OpFunc
	opCatalogUpdate  OpFunc
	catalogOffer     bool
	opBackfill       BackfillFunc
	opWatchAdd       WatchAddFunc
	opWatchImport    WatchImportFunc
	opDeckAdd        DeckAddFunc
	opDeckAddFile    DeckAddFileFunc
	opImport         ImportFunc
	op               *opState
	opGen            int

	undoStack *undoAction

	view     viewMode
	movers   []store.PriceChange
	unpriced []store.UnpricedRow

	overs  []store.WatchStatus
	unders []store.WatchStatus

	watchSortIdx   [watchSectionCount]int
	watchSortRev   [watchSectionCount]bool
	watchSecOffset [watchSectionCount]int

	allMovers      []store.PriceChange
	filteredMovers []store.PriceChange
	moversPage     int
	allUnpriced    []store.UnpricedRow
	allWatches     []store.WatchStatus

	moversCache    map[int][]store.PriceChange
	moversCacheGen int
	trendCache     map[trendKey]trendPair
	trendCacheGen  int
	trendReading   trendAttempt
	trendFailed    trendAttempt
	dataGen        int

	showPennies bool
	pennyLimit  float64

	entryIndex   map[int64]map[string]int
	viewEligible map[int64]bool

	displacedContainer int64

	collapsed map[int64]bool

	containerNameW int

	marketSortIdx [3]int
	marketSortRev [3]bool
	marketComps   []market.Comp
	compsSortIdx  int
	compsSortRev  bool

	marketAllRows  []market.Row
	marketAllComps []market.Comp
	marketPage     [3]int

	marketSecOffset [3]int

	compsBuySide bool

	liquidLowball bool

	moversDaysIdx int
	hasCostBasis  bool

	marketFetch  MarketFunc
	marketCached MarketCachedFunc
	cardComps    CardCompFunc
	openURL      OpenURLFunc
	updateCheck  UpdateCheckFunc
	printSearch  PrintSearchFunc
	cardDocument CardDocumentFunc

	historyBackfill HistoryBackfillFunc
	setPrints       SetPrintsFunc

	setUnowned bool
	lastSet    string
	curve      store.Curve

	setOwned, setTotal int
	setMissingCost     float64
	marketResult       market.Result
	marketRows         []market.Row
	marketLoading      bool
	marketPrefetch     bool
	marketLoaded       bool
	marketGen          int
	marketCancel       context.CancelFunc
	spinner            spinner.Model

	ctx context.Context

	clock func() time.Time

	detail *detail

	preview    previewArt
	previewGen int

	detailComps map[string]compsResult

	text *textView

	reportFn ReportFunc

	exportFn ExportFunc

	confirmCh   <-chan ConfirmRequest
	deferredAsk *ConfirmRequest

	newAddChild func() (tui.Child, error)
	addChild    *tui.Child
	addPending  tui.Pending
	addSummary  tui.Summary

	helpRowsMemo map[helpRowsKey]int

	liveVersion int64
	liveKnown   bool
	liveGen     int
	livePending bool
	liveOff     bool
	liveGone    bool
	liveMissed  int

	rowGone bool

	status    string
	statusErr bool

	err error
}

func New(st Store, opts ...Option) (Model, error) {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	m := Model{store: st, focus: paneContainers, spinner: sp, ctx: context.Background(),
		env: ui.Detect(os.Stdout), theme: ui.DefaultTheme(), imgTier: ui.DetectImageTier(),
		cellAspect: ui.CellAspectOverride(), setsMode: true,
		commands: commands(), pennyLimit: defaultPennyLimit,
		helpRowsMemo: map[helpRowsKey]int{},
		selAnchor:    noSelection}
	for _, opt := range opts {
		opt(&m)
	}
	m.loadPennyFilters()
	m.loadSettlingWindow()
	m.loadFoldState()
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

type catalogFirstRunMsg struct{}

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

func pricePoints(s []store.PricePoint) []ui.TimePoint {
	out := make([]ui.TimePoint, len(s))
	for i, p := range s {
		out[i] = ui.TimePoint{AsOf: p.AsOf, Value: p.Price}
	}
	return out
}

func (m *Model) loadContainers() error {
	held, err := m.store.HasCostBasis()
	if err != nil {
		return fmt.Errorf("looking for a cost basis: %w", err)
	}
	if m.hasCostBasis = held; !held {
		m.moversDaysIdx %= len(moversWindowDays)
	}
	if m.setsMode {
		return m.loadSetContainers()
	}
	binders, err := m.store.ListBinders()
	if err != nil {
		return fmt.Errorf("reading binders: %w", err)
	}
	decks, err := m.store.ListDecks()
	if err != nil {
		return fmt.Errorf("reading decks: %w", err)
	}
	folders, err := m.store.ListFolders()
	if err != nil {
		return fmt.Errorf("reading folders: %w", err)
	}

	out := make([]container, 0, len(binders)+len(decks)+len(folders)+1)

	all := container{ID: allCardsID, Name: allCardsName, Kind: kindAllCards}
	widest := nameWidth(all)
	out = append(out, all)
	for _, b := range binders {
		widest = max(widest, ui.Width(b.Name))
		out = append(out, container{
			ID: b.ID, Name: b.Name, Kind: store.KindCollection,
			Copies: b.TotalCopies, Value: b.Value, isDefault: b.IsDefault,
			Counted: b.Counted,
		})
		if !b.Counted {
			continue
		}
		out[0].Copies += b.TotalCopies
		out[0].Value += b.Value
	}
	for _, d := range decks {
		if !d.Counted {
			continue
		}
		out[0].Copies += d.TotalCopies
		out[0].Value += d.Value
	}
	tree, treeWidest := deckTree(folders, decks, m.collapsed)
	out = append(out, tree...)
	m.containerNameW = max(widest, treeWidest)
	m.containers = out
	m.clampCursor(paneContainers)
	return nil
}

func deckContainer(d store.DeckSummary) container {
	return container{
		ID: d.ID, Name: d.Name, Kind: store.KindDeck,
		Copies: d.TotalCopies, Value: d.Value, parentID: d.ParentID,
		Counted: d.Counted,
		meta: store.DeckMeta{
			Name: d.Name, Source: d.Source, SourceID: d.SourceID,
			SourceURL: d.SourceURL, Format: d.Format,
		},
	}
}

func deckTree(folders, decks []store.DeckSummary, collapsed map[int64]bool) ([]container, int) {
	known := make(map[int64]bool, len(folders))
	for _, f := range folders {
		known[f.ID] = true
	}
	var top []container
	widest := 0
	children := map[int64][]container{}
	for _, d := range store.DecksByValue(decks) {
		c := deckContainer(d)
		if !known[d.ParentID] {
			c.parentID = 0
			widest = max(widest, nameWidth(c))
			top = append(top, c)
			continue
		}
		c.depth = 1

		widest = max(widest, nameWidth(c))
		children[d.ParentID] = append(children[d.ParentID], c)
	}
	for _, f := range folders {
		c := container{
			ID: f.ID, Name: f.Name, Kind: kindFolder,
			Copies: f.TotalCopies, Value: f.Value,
		}
		widest = max(widest, nameWidth(c))
		top = append(top, c)
	}
	slices.SortFunc(top, func(a, b container) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	out := make([]container, 0, len(top)+len(decks))
	for _, c := range top {
		out = append(out, c)
		if c.Kind == kindFolder && !collapsed[c.ID] {
			out = append(out, children[c.ID]...)
		}
	}
	return out, widest
}

func nameWidth(c container) int {
	return 2*c.depth + ui.Width(c.Name)
}

func (m *Model) loadCards() error {
	m.clearSelection()
	sel := m.selectedContainer()
	if sel == nil {
		m.cards = nil
		return nil
	}

	m.curve = store.Curve{}

	var out []card
	switch sel.Kind {
	case kindSet:
		cards, err := m.setCards(sel.setCode)
		if err != nil {
			return fmt.Errorf("reading %q: %w", sel.Name, err)
		}
		out = cards
	case kindAllCards, store.KindCollection, kindFolder:
		var rows []store.CollectionRow
		var err error
		switch sel.Kind {
		case kindAllCards:
			rows, err = m.store.AllByFinish()
		case kindFolder:
			rows, err = m.store.FolderByFinish(sel.ID)
		default:
			rows, err = m.store.BinderByFinish(sel.ID)
		}
		if err != nil {
			return fmt.Errorf("reading %q: %w", sel.Name, err)
		}
		out = collectionCards(rows)
		if sel.Kind == kindAllCards {

			out = mergeByName(out)
			if err := m.labelHeldIn(out); err != nil {
				return fmt.Errorf("reading where %q is held: %w", sel.Name, err)
			}
		}
	default:
		entries, err := m.store.DeckEntries(sel.ID)
		if err != nil {
			return fmt.Errorf("reading deck %q: %w", sel.Name, err)
		}
		if m.curve, err = m.store.DeckCurve(sel.ID, store.BoardMain); err != nil {
			return fmt.Errorf("reading the curve of %q: %w", sel.Name, err)
		}
		for _, e := range store.EntriesByValue(entries) {
			out = append(out, card{
				ScryfallID: e.Card.ScryfallID, Name: e.Card.Name, SetCode: e.Card.SetCode,
				CollectorNumber: e.Card.CollectorNumber, Finish: e.Finish,
				Condition: e.Condition, Board: e.Board,
				Quantity: e.Quantity, Price: e.Price(), Value: e.Value(),
				AltSource: e.Card.AltSource, ColorIdentity: e.Card.ColorIdentity,
				Treatment: e.Card.Treatment,
			})
		}
	}

	m.allCards = out
	m.sortHoldings()
	m.cardsPage = 0
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
	return nil
}

func mergeByName(rows []card) []card {
	idx := map[string]int{}
	var out []card
	for _, c := range rows {
		key := nameFinishKey(c.Name, c.Finish)
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

func (m *Model) applyFilter() {
	if m.filter.Empty() && m.floorMin() == 0 {

		m.filteredCards = m.allCards
	} else {
		out := make([]card, 0, len(m.allCards))
		for _, c := range m.allCards {
			if m.filter.Matches(c.subject(), m.allowed) && !m.underFloor(c.Price) {
				out = append(out, c)
			}
		}
		m.filteredCards = out
	}
	m.cardsColW = measureCardCols(m.filteredCards)
	m.deriveCardsPage()
	m.clampCursor(paneCards)
	m.refreshEmptyNote()
}

func (m *Model) deriveCardsPage() {
	lo, hi := pageBounds(&m.cardsPage, len(m.filteredCards))
	m.cards = m.filteredCards[lo:hi]
}

func (m *Model) deriveMoversPage() {
	lo, hi := pageBounds(&m.moversPage, len(m.filteredMovers))
	m.movers = m.filteredMovers[lo:hi]
}

type cardColWidths struct{ name, set, fin, qty, price, value int }

type moverColWidths struct{ name, set, fin, from, was, now, change, qty, impact int }

func measureCardCols(rows []card) cardColWidths {
	var w cardColWidths
	for _, c := range rows {
		w.name = max(w.name, ui.Width(c.Name))
		w.set = max(w.set, ui.Width(ui.Printing(c.SetCode, c.CollectorNumber)))
		w.fin = max(w.fin, ui.Width(ui.FinishTreated(c.Finish, c.Treatment)))
		w.qty = max(w.qty, ui.Width(ui.Qty(c.Quantity)))
		w.price = max(w.price, ui.Width(ui.MoneyPtr(c.Price)))
		w.value = max(w.value, ui.Width(ui.Money(c.Value)))
	}
	return w
}

func measureMoverCols(rows []store.PriceChange, cutoff time.Time) moverColWidths {
	var w moverColWidths
	for _, c := range rows {
		w.name = max(w.name, ui.Width(c.Name))
		w.set = max(w.set, ui.Width(ui.Printing(c.SetCode, c.CollectorNumber)))
		w.fin = max(w.fin, ui.Width(ui.FinishTreated(c.Finish, c.Treatment)))
		w.from = max(w.from, ui.Width(c.BaselineFrom(cutoff)))
		w.was = max(w.was, ui.Width(ui.Money(c.Old)))
		w.now = max(w.now, ui.Width(ui.Money(c.New)))
		w.change = max(w.change, ui.Width(ui.SignedPercent(c.Pct())))
		w.qty = max(w.qty, ui.Width(ui.Qty(c.Copies)))
		w.impact = max(w.impact, ui.Width(ui.SignedMoney(c.TotalDelta())))
	}
	return w
}

func stableNameWidth(measured, paneWidth int) int {
	return min(measured, max(16, paneWidth/3))
}

func pageBounds(page *int, tot int) (lo, hi int) {
	maxPage := 0
	if tot > 0 {
		maxPage = (tot - 1) / singleTablePageSize
	}
	*page = min(max(*page, 0), maxPage)
	lo = min(*page*singleTablePageSize, tot)
	return lo, min(lo+singleTablePageSize, tot)
}

var floorLevels = []float64{0, 5, 10, 25, 50, 100}

func (m Model) floorMin() float64 { return floorLevels[m.floorIdx] }

func (m Model) underFloor(p *float64) bool {
	if min := m.floorMin(); min > 0 {
		return p == nil || *p < min
	}
	return false
}

func (m *Model) cycleFloor() {
	m.floorIdx = (m.floorIdx + 1) % len(floorLevels)

	m.cardsPage, m.moversPage = 0, 0
	m.applyFilter()
	m.deriveView()
	m.clampCursor(paneCards)
	if min := m.floorMin(); min > 0 {
		m.status, m.statusErr = fmt.Sprintf("floor %s · hiding cards worth less", ui.Money(min)), false
	} else {
		m.status, m.statusErr = "floor off", false
	}
}

func (m *Model) refreshEmptyNote() {
	m.emptyNote = ""

	rows := m.viewRowCount()
	if m.view == viewWatches {

		rows = m.watchTotalRows()
	}
	if rows > 0 || m.filter.Empty() || !m.filter.NeedsCatalog() {
		return
	}
	enriched, total, err := m.store.EnrichedCount()
	if err != nil || total == 0 {
		return
	}
	switch {
	case enriched == 0:
		m.emptyNote = "no card details stored yet · press : and run UpdatePrices to filter by rarity, type or colour"
	case enriched < total:
		m.emptyNote = fmt.Sprintf("no matches · %d of %d cards have details; update-prices fills the rest",
			enriched, total)
	default:
		m.emptyNote = "no matches"
	}
}

func (m *Model) setFilter(text string) {
	f, err := cardfilter.Parse(text)
	if err != nil {

		m.filterErr = err.Error()
		return
	}
	m.filterErr = ""
	m.filter = f
	m.allowed = nil

	if f.NeedsCatalog() {
		ids, err := m.store.MatchingCardIDs(f.Traits())
		if err != nil {
			m.setError(err)
			return
		}
		m.allowed = ids
	}

	m.cardsPage, m.moversPage = 0, 0

	m.deriveView()
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
}

func (m *Model) clearFilter() {
	m.filtering = false
	m.filterText = ""
	m.filterErr = ""
	m.filter = cardfilter.Filter{}
	m.allowed = nil
	m.cardsPage, m.moversPage = 0, 0
	m.deriveView()
	m.applyFilter()
	m.cursor[paneCards] = 0
	m.offset[paneCards] = 0
}

func (m Model) selectedContainer() *container {
	if len(m.containers) == 0 {
		return nil
	}
	i := min(max(m.cursor[paneContainers], 0), len(m.containers)-1)
	return &m.containers[i]
}

func (m Model) selectedCard() *card {
	if len(m.cards) == 0 {
		return nil
	}
	i := min(max(m.cursor[paneCards], 0), len(m.cards)-1)
	return &m.cards[i]
}

func (m Model) rowCount(p pane) int {
	if p == paneContainers {
		return len(m.containers)
	}
	return m.viewRowCount()
}

func (m *Model) clampCursor(p pane) {
	n := m.rowCount(p)
	if n == 0 {
		m.cursor[p], m.offset[p] = 0, 0
		return
	}
	m.cursor[p] = min(max(m.cursor[p], 0), n-1)
}

func (m Model) Init() tea.Cmd {

	init := tea.Batch(awaitConfirm(m.ctx, m.confirmCh), livePoll(),
		func() tea.Msg { return trendPrefetchStartMsg{} })
	if m.marketFetch != nil {
		init = tea.Batch(init, func() tea.Msg { return marketPrefetchStartMsg{} })
	}

	if m.updateCheck != nil {
		init = tea.Batch(init, func() tea.Msg { return updateCheckStartMsg{} })
	}

	if m.catalogOffer && m.opCatalogUpdate != nil {
		return tea.Batch(init, func() tea.Msg { return catalogFirstRunMsg{} })
	}
	return init
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	const wheelStep = 3

	if m.detail != nil {

		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.detail.scroll = max(m.detail.scroll-wheelStep, 0)
		case tea.MouseButtonWheelDown:
			m.detail.scroll += wheelStep
		}
		return m, nil
	}

	var key tea.KeyMsg
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		key = tea.KeyMsg{Type: tea.KeyUp}
	case tea.MouseButtonWheelDown:
		key = tea.KeyMsg{Type: tea.KeyDown}
	default:

		return m, nil
	}
	cur := m
	var cmds []tea.Cmd
	for range wheelStep {
		next, cmd := cur.handleKey(key)
		cmds = append(cmds, cmd)

		n, ok := next.(Model)
		if !ok {
			return next, tea.Batch(cmds...)
		}
		cur = n
	}
	return cur, tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	had := m.detail != nil
	next, cmd := m.update(msg)
	n, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if c := n.syncPreview(); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := n.syncTrends(); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	switch now := n.detail != nil; {
	case now && !had:
		return n, tea.Batch(cmd, tea.EnableMouseCellMotion)
	case !now && had:
		return n, tea.Batch(cmd, tea.DisableMouse)
	}
	return n, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.scrollIntoView()
		if m.addChild != nil {
			child, _ := m.addChild.Update(msg)
			m.addChild = &child
		}
		var cmds []tea.Cmd

		if d := m.detail; d != nil && !d.imagePending && len(d.image) > 0 &&
			d.imageColsDrawn != m.detailImageCols() {
			cmds = append(cmds, m.fetchDetailImage())
		}

		if d := m.detail; d != nil && (d.imageTransmit != "" || d.imagePending) {
			cmds = append(cmds, m.transmitSettle())
		}
		return m, tea.Batch(cmds...)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case marketMsg:
		return m.onMarket(msg)
	case imageMsg:
		return m.onImage(msg)
	case detailCompsMsg:
		return m.onDetailComps(msg)
	case cardDocumentMsg:
		return m.onCardDocument(msg)
	case historyMsg:
		return m.onHistory(msg)
	case updateCheckStartMsg:
		return m, m.checkForUpdate()
	case updateCheckMsg:
		return m.onUpdateCheck(msg)
	case catalogFirstRunMsg:
		return m, m.startOp("updating the catalog", m.opCatalogUpdate)
	case opProgressMsg:
		return m.onOpProgress(msg)
	case opDoneMsg:

		return m.onOpDone(msg)
	case opConfirmMsg:
		return m.onOpConfirm(msg)
	case retransmitMsg:
		return m.onRetransmit(msg)
	case previewDueMsg:
		return m.onPreviewDue(msg)
	case previewSentMsg:
		return m.onPreviewSent(msg)
	case livePollMsg:
		return m.onLivePoll()
	case liveQuietMsg:
		return m.onLiveQuiet(msg)
	case marketPrefetchStartMsg:
		m.marketPrefetch = true
		return m, m.prefetchMarket()
	case marketPrefetchMsg:
		return m.onMarketPrefetch(msg)
	case trendPrefetchStartMsg:
		return m, m.prefetchTrends()
	case trendPrefetchMsg:
		return m.onTrendPrefetch(msg)
	case spinner.TickMsg:

		var cmds []tea.Cmd
		if m.marketBusy() || m.op != nil || m.trendBusy() {
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

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":

		m.stageQuit()
	case "esc", "backspace":
		m.detail = nil
		m.detailComps = nil
	case "enter":

		if m.detail.zone == zoneHeld {
			m.editHeldField()
			return m, nil
		}

		if m.openURL != nil && len(m.detail.links) > 0 {
			l := m.detail.links[m.detail.linkCursor]
			if err := m.openURL(l.url); err != nil {
				m.setError(err)
			}
			return m, nil
		}
		m.detail = nil
	case "tab":

		if m.detail.zone == zoneHeld {
			if m.detail.heldField < heldFieldCount-1 {
				m.detail.heldField++
				break
			}

			m.detail.zone = zoneLinks
			m.detail.linkCursor = 0
			m.detail.scroll = 1 << 20
			break
		}
		if n := len(m.detail.links); m.detail.linkCursor < n-1 {
			m.detail.linkCursor++
			break
		}

		if len(m.detail.holdings) > 0 {
			m.detail.zone = zoneHeld
			m.detail.heldField = 0
			m.detail.scrollHeldIntoView = true
		}
	case "shift+tab":

		if m.detail.zone == zoneHeld {
			if m.detail.heldField > 0 {
				m.detail.heldField--
				break
			}

			m.detail.zone = zoneLinks
			m.detail.linkCursor = max(len(m.detail.links)-1, 0)
			m.detail.scroll = 1 << 20
			break
		}
		if m.detail.linkCursor > 0 {
			m.detail.linkCursor--
			break
		}
		if len(m.detail.holdings) > 0 {
			m.detail.zone = zoneHeld
			m.detail.heldField = heldFieldCount - 1
			m.detail.scrollHeldIntoView = true
		}
	case "up", "k":

		if m.detail.zone == zoneLinks && len(m.detail.holdings) > 0 {
			m.detail.zone = zoneHeld
			m.detail.scrollHeldIntoView = true
			return m, nil
		}
		return m, m.moveHeldCursor(-1)
	case "down", "j":
		if m.detail.zone == zoneHeld {
			if m.detail.heldCursor >= len(m.detail.holdings)-1 {

				m.detail.zone = zoneLinks
				return m, nil
			}
			return m, m.moveHeldCursor(1)
		}
	case "left", "h":
		if m.detail.zone == zoneHeld {
			m.detail.heldField = max(m.detail.heldField-1, 0)
		} else if n := len(m.detail.links); n > 0 {
			m.detail.linkCursor = max(m.detail.linkCursor-1, 0)
		}
	case "right", "l":
		if m.detail.zone == zoneHeld {
			m.detail.heldField = min(m.detail.heldField+1, heldFieldCount-1)
		} else if n := len(m.detail.links); n > 0 {
			m.detail.linkCursor = min(m.detail.linkCursor+1, n-1)
		}
	case "+", "=":

		return m, m.adjustHeldQuantity(1)
	case "-", "_":
		return m, m.adjustHeldQuantity(-1)
	case "d":
		m.askHeldRemoval()
	case "f":

		if !m.detail.flippable() {
			return m, nil
		}
		m.detail.showBack = !m.detail.showBack
		m.detail.image = nil
		m.detail.imageTransmit = ""
		return m, m.fetchDetailImage()
	case "pgup":

		m.detail.scroll = max(m.detail.scroll-max(m.visibleRows()-1, 1), 0)
	case "pgdown":

		m.detail.scroll += max(m.visibleRows()-1, 1)
	case ":", "ctrl+p":

		m.openPalette()
	}
	return m, nil
}

func (m Model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {

	if cmd, ok := m.runCommand(msg.String()); ok {
		return m, cmd
	}
	switch msg.String() {

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
			comps := m.openDetail()
			return m, tea.Batch(m.fetchDetailImage(), comps)
		} else {

			m.focus = paneCards
		}
		return m, nil

	case "/":
		m.filtering = true
		m.focus = paneCards
		m.status = ""
		return m, nil
	case "esc":
		if _, _, ok := m.selectionRange(); ok {
			m.clearSelection()
			m.status, m.statusErr = "selection cleared", false
			return m, nil
		}
		if m.watchPick {

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
		if !m.filter.Empty() {
			m.clearFilter()
			m.status = "filter cleared"
			return m, nil
		}
		if m.op != nil {

			m.cancelOp()
			return m, nil
		}

		m.stageQuit()
		return m, nil

	case "tab":

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

	case " ":
		m.toggleFold()
		return m, nil
	case "x":
		m.toggleCounted()
	case "shift+down":
		m.extendSelection(1)
	case "shift+up":
		m.extendSelection(-1)
	case "up", "k":
		m.move(-1)
		m.status = ""
		return m, nil
	case "down", "j":
		m.move(1)
		m.status = ""
		return m, nil
	case "pgup":
		m.page(-1)
		m.status = ""
		return m, nil
	case "pgdown":
		m.page(1)
		m.status = ""
		return m, nil
	case "home", "g":
		m.moveTo(0)
		m.status = ""
		return m, nil
	case "end", "G":
		m.moveTo(m.rowCount(m.focus) - 1)
		m.status = ""
		return m, nil

	}
	return m, nil
}

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

		if sel.Kind == kindSet {
			m.status, m.statusErr = "a set is how cards were printed, not where they live · remove cards from their binders", true
			return
		}
		if sel.Kind == kindFolder {
			m.status, m.statusErr = "a folder groups decks · remove it with `hoard folder rm`", true
			return
		}
		if sel.Kind == store.KindCollection {
			if sel.isDefault {
				m.status, m.statusErr = "the default binder cannot be removed", true
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

	if w := m.selectedWatch(); w != nil {
		m.askWatchRemoval(*w)
		return
	}
	if m.view == viewWatches {

		if m.selectedUnpricedRow() != nil {
			m.status, m.statusErr = "d removes a watch · this row is a card you hold, not a watch", true
		}
		return
	}

	if sel := m.selectedContainer(); m.view == viewHoldings && sel != nil && sel.Kind == kindSet {
		m.askSetRemoval()
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
		prompt: fmt.Sprintf("remove %s from %s?", c.Name, m.selectedContainer().Name),
		help:   "y remove · any other key cancels",
		onYes:  func(m *Model) tea.Cmd { m.removeCard(); return nil },
	}
}

func (m *Model) stageQuit() {
	if m.op != nil {
		title := m.op.title
		m.confirm = &pendingConfirm{
			prompt: title + " is still running. Quit anyway?",
			help:   "y quit · any other key stays",
			onYes: func(m *Model) tea.Cmd {

				m.declineDeferredAsk()
				m.cancelOp()
				return tea.Quit
			},
		}
		return
	}
	m.confirm = &pendingConfirm{
		prompt: "quit hoard?",
		help:   "y quit · any other key stays",
		onYes: func(m *Model) tea.Cmd {
			m.declineDeferredAsk()
			return tea.Quit
		},
	}
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {

		if m.confirm.onNo != nil {
			m.confirm.onNo(&m)
		}
		m.declineDeferredAsk()
		m.cancelOp()
		return m, tea.Quit
	}
	c := m.confirm
	m.confirm = nil

	var cmd tea.Cmd
	if msg.String() != "y" {

		m.status, m.statusErr = "cancelled", false
		if c.onNo != nil {
			c.onNo(&m)
		}
	} else {
		cmd = c.onYes(&m)
	}

	if m.deferredAsk != nil {
		req := *m.deferredAsk
		m.deferredAsk = nil
		m.stageConfirmRequest(req)
	}
	return m, cmd
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:

		m.clearFilter()
		if m.watchPick {
			m.watchPick = false
			cmd := m.showView(viewWatches)
			m.status, m.statusErr = "watch cancelled", false
			return m, cmd
		}
		return m, nil
	case tea.KeyTab:

		m.filtering = false
		if m.filter.Empty() {
			m.filterText = ""
		}
		m.focus = paneContainers
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		if m.filter.Empty() {
			m.filterText = ""
		}

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

	case tea.KeyUp:
		m.move(-1)
		return m, nil
	case tea.KeyDown:
		m.move(1)
		return m, nil
	}
	return m, nil
}

func (m *Model) move(delta int) {
	m.clearSelection()
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

func (m *Model) page(dir int) {
	n := m.rowCount(m.focus)
	if n == 0 {
		return
	}
	rows := max(m.paneRows(m.focus)-1, 1)
	step := dir * max(rows-1, 1)
	m.offset[m.focus] = min(max(m.offset[m.focus]+step, 0), max(n-rows, 0))
	m.move(step)
}

func (m *Model) moveTo(target int) {
	m.clearSelection()
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

func (m *Model) onCursorMoved() {
	m.scrollIntoView()
	if m.focus != paneContainers {
		return
	}
	m.displacedContainer = 0
	if err := m.loadCards(); err != nil {
		m.setError(err)
		return
	}
	m.deriveView()
}

func (m *Model) setError(err error) {
	m.status = err.Error()
	m.statusErr = true
}
