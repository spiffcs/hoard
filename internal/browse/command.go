package browse

// The command registry: every deliberate action the browser offers, defined
// once. Direct keybindings and the palette both read this table, so the
// palette's key-hint column can never drift from what the keys actually do,
// and a new capability added here appears in both surfaces for free.
//
// Named command rather than action: internal/action is the capability layer
// this package deliberately does not import, and one word doing two jobs
// would make every conversation about either ambiguous.
//
// Navigation (cursor movement, pane focus, paging) is not in the registry:
// moving around is not a command you'd search for, and the palette listing
// "down" would be noise.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type command struct {
	id string
	// title is what the palette shows and fuzzy-matches. Plain verbs, no
	// trailing ellipsis: nearly every command here asks a question or opens
	// a prompt, so the marker distinguished nothing and read as clutter.
	title string
	// aliases are extra fuzzy fodder, never displayed.
	aliases string
	// desc is the one-line explanation shown under the palette's help line
	// while the command is highlighted. Every palette-visible command has
	// one; unhidden commands without a desc render nothing there.
	desc string
	// key is the direct binding, shown as the palette's hint column; ""
	// means palette-only.
	key string
	// hidden keeps a command out of the palette while its key keeps
	// working — for the pure key reflexes (sort, floor, view cycling) the
	// palette listing was noise beside the verbs that do something.
	hidden bool
	// hide is hidden's contextual sibling: true keeps an applicable
	// command out of the palette right now, key still bound. For commands
	// that earn their listing on some views and are noise on others —
	// where would unbind the key along with the listing. Nil means never.
	hide func(*Model) bool
	// where reports whether the command applies right now; nil means
	// always. Hidden commands are absent from the palette and their keys
	// fall through. Finer contextual refusals belong in run as status
	// messages — the palette must not need a second copy of every guard.
	where func(*Model) bool
	// rank orders the empty-query palette: higher first, so the commands
	// that help the current view lead the list. Nil means 0. A fuzzy query
	// overrides it entirely — typing means the user knows what they want.
	rank func(*Model) int
	// run performs the command. It may stage a confirm or a prompt.
	run func(*Model) tea.Cmd
}

// commands is the registry, in palette display order: the everyday verbs
// first, view jumps after.
func commands() []command {
	return []command{
		{
			id: "add", title: "AddCards", aliases: "scan camera new card",
			desc: "Open the add flow: type a card name, or scan with your iPhone.",
			key:  "a",
			rank: onView(viewHoldings, 3),
			// The cascade runs as an embedded child takeover, so ops keep
			// running behind it and nothing quits.
			run: func(m *Model) tea.Cmd { return m.openAddCascade() },
		},
		{
			id: "remove", title: "RemoveSelected", aliases: "delete card deck",
			desc: "Remove the selected card or deck, after a y/n confirm.",
			key:  "d",
			// Off the watches and market palettes; on watches the key still
			// removes the watch under the cursor.
			hide: func(m *Model) bool { return m.view == viewWatches || m.view == viewMarket },
			run:  func(m *Model) tea.Cmd { m.askRemoval(); return nil },
		},
		{
			// No where guard: an empty undo stack answers "nothing to undo"
			// through run — the feedback is the point, and hiding the command
			// would swallow it.
			id: "undo", title: "UndoLastEdit", aliases: "revert restore",
			desc: "Put back whatever the last quantity edit or removal changed.",
			key:  "u",
			hide: func(m *Model) bool { return m.view == viewWatches || m.view == viewMarket },
			run:  func(m *Model) tea.Cmd { m.undoRecorded(); return nil },
		},
		{
			id: "sort", aliases: "order by",
			key: "s", hidden: true,
			run: func(m *Model) tea.Cmd {
				if m.view != viewMarket {
					// Page first, then sort: the new order's first rows
					// live on page one (market resets its own inside
					// cycleSort).
					m.cardsPage, m.moversPage = 0, 0
				}
				m.cycleSort()
				if m.view != viewMarket {
					m.cursor[paneCards], m.offset[paneCards] = 0, 0
				}
				m.status, m.statusErr = "sorted by "+m.sortLabel(), false
				return nil
			},
		},
		{
			id: "sort.reverse", aliases: "order descending ascending",
			key: "S", hidden: true,
			run: func(m *Model) tea.Cmd {
				if m.view != viewMarket {
					m.cardsPage, m.moversPage = 0, 0
				}
				m.reverseSort()
				if m.view != viewMarket {
					m.cursor[paneCards], m.offset[paneCards] = 0, 0
				}
				m.status, m.statusErr = "sorted by "+m.sortLabel(), false
				return nil
			},
		},
		{
			id: "floor.cycle", aliases: "mask value filter cheap junk minimum",
			key: "M", hidden: true,
			run: func(m *Model) tea.Cmd { m.cycleFloor(); return nil },
		},
		{
			id: "reload", title: "ReloadFromDatabase", aliases: "refresh",
			desc: "Re-read everything from disk, keeping your place.",
			key:  "r",
			run:  func(m *Model) tea.Cmd { m.reload(); return nil },
		},
		{
			id: "view.next", aliases: "cycle switch",
			key: "v", hidden: true,
			run: func(m *Model) tea.Cmd { return m.showView(m.view.next()) },
		},
		{
			id: "op.update-prices", title: "UpdatePrices", aliases: "refresh fetch scryfall daily",
			desc:  "Fetch today's prices from Scryfall for every card you hold.",
			where: func(m *Model) bool { return m.opUpdatePrices != nil },
			rank: func(m *Model) int {
				switch {
				case m.view == viewMovers && len(m.movers) == 0:
					return 5
				case m.view == viewMovers, m.view == viewUnpriced, m.view == viewWatches:
					return 3
				}
				return 2
			},
			run: func(m *Model) tea.Cmd { return m.startOp("updating prices", m.opUpdatePrices) },
		},
		{
			id: "op.backfill", title: "BackfillPriceHistory30",
			aliases: "backdate movers history mtgjson import past",
			// The archive's size lives in the description as prose, not a
			// number — a hardcoded figure goes stale the day MTGJSON grows.
			desc:  "Download MTGJSON's daily price archive (a large file) and record the past 30 days.",
			where: func(m *Model) bool { return m.opBackfill != nil },
			rank: func(m *Model) int {
				if m.view == viewMovers {
					if len(m.movers) == 0 {
						return 5
					}
					return 3
				}
				return onView(viewHoldings, 1)(m)
			},
			run: func(m *Model) tea.Cmd { return m.startBackfill(30) },
		},
		{
			// The deeper window lives beside the movers view it feeds; the
			// collection palette keeps the one everyday choice.
			id: "op.backfill.90", title: "BackfillPriceHistory90",
			aliases: "backdate movers history mtgjson import past quarter",
			desc:    "Download MTGJSON's daily price archive (a large file) and record the past 90 days.",
			where:   func(m *Model) bool { return m.opBackfill != nil && m.view == viewMovers },
			rank:    func(*Model) int { return 2 },
			run:     func(m *Model) tea.Cmd { return m.startBackfill(90) },
		},
		{
			id: "op.repair-finishes", title: "RepairFinishes", aliases: "fix foil unpriced zero",
			desc:  "Move cards stored in a finish their printing lacks onto one it has.",
			key:   "f",
			where: func(m *Model) bool { return m.opRepairFinishes != nil },
			// Listed where its symptom shows — the unpriced view and the
			// collection — not on movers or watches, whose rows are all
			// priced. The card detail keeps it: its palette is the price
			// refreshers. The f key works everywhere regardless.
			hide: func(m *Model) bool {
				return m.detail == nil && (m.view == viewMovers || m.view == viewWatches)
			},
			rank: onView(viewUnpriced, 4),
			run:  func(m *Model) tea.Cmd { return m.startOp("repairing finishes", m.opRepairFinishes) },
		},
		{
			id: "op.catalog-update", title: "UpdateCardCatalog", aliases: "download bundle scryfall rebuild",
			desc:  "Download Scryfall's card bundle so lookups answer instantly and offline.",
			where: func(m *Model) bool { return m.opCatalogUpdate != nil },
			run:   func(m *Model) tea.Cmd { return m.startOp("updating the catalog", m.opCatalogUpdate) },
		},
		{
			id: "deck.add-url", title: "AddDeckByURL", aliases: "import archidekt moxfield deckstats link",
			desc:  "Fetch a deck list from its link and import it as a deck.",
			where: func(m *Model) bool { return m.opDeckAdd != nil },
			rank:  onView(viewHoldings, 2),
			run:   func(m *Model) tea.Cmd { m.promptDeckURL(); return nil },
		},
		{
			id: "export.container", title: "ExportThisContainer", aliases: "csv save backup moxfield archidekt",
			desc: "Write the selected binder or deck to a CSV or JSON file.",
			where: func(m *Model) bool {
				return m.exportFn != nil && m.view == viewHoldings && m.selectedContainer() != nil
			},
			rank: onView(viewHoldings, 1),
			run: func(m *Model) tea.Cmd {
				sel := m.selectedContainer()
				if sel == nil {
					return nil
				}
				// The merged view is everything, so exporting it is the
				// export-everything flow under this container's name.
				if sel.Kind == kindAllCards {
					m.promptExport("", "", "hoard-export")
					return nil
				}
				// A set is a lens, not a container: its synthetic id would
				// export as a binder ref that matches nothing.
				if sel.Kind == kindSet {
					m.status, m.statusErr = "sets can't be exported directly · ExportEverything writes the whole hoard", true
					return nil
				}
				// Refs by id, not name: a name is a fragment match and can
				// be ambiguous; the id under the cursor never is.
				ref := strconv.FormatInt(sel.ID, 10)
				binderRef, deckRef := "", ""
				if sel.Kind == store.KindDeck {
					deckRef = ref
				} else {
					binderRef = ref
				}
				m.promptExport(binderRef, deckRef, slugify(sel.Name))
				return nil
			},
		},
		{
			id: "export.all", title: "ExportEverything", aliases: "csv save backup all holdings",
			desc:  "Write every holding to one file.",
			where: func(m *Model) bool { return m.exportFn != nil },
			run:   func(m *Model) tea.Cmd { m.promptExport("", "", "hoard-export"); return nil },
		},
		{
			id: "import.file", title: "ImportCollectionCSV", aliases: "manabox moxfield delver csv file",
			desc:  "Add a collection export from ManaBox, Moxfield, Delver Lens or hoard.",
			where: func(m *Model) bool { return m.opImport != nil },
			rank:  onView(viewHoldings, 2),
			run:   func(m *Model) tea.Cmd { m.promptImportPath(); return nil },
		},
		{
			id: "report.view", title: "ValuationReport", aliases: "insurance worth top holdings dated",
			desc:  "A dated valuation: totals, per-binder breakdown, top holdings, price sources.",
			where: func(m *Model) bool { return m.reportFn != nil },
			run: func(m *Model) tea.Cmd {
				lines, err := m.reportFn(10, m.width)
				if err != nil {
					m.setError(err)
					return nil
				}
				m.openText("valuation report", lines)
				return nil
			},
		},
		{
			id: "op.cancel", title: "CancelOperation", aliases: "stop abort",
			desc:  "Stop the operation currently running, keeping what it finished.",
			where: func(m *Model) bool { return m.op != nil },
			rank:  func(*Model) int { return 10 },
			run:   func(m *Model) tea.Cmd { m.cancelOp(); return nil },
		},
		{
			id: "market.fetch", title: "FetchVendorPrices", aliases: "quotes compare buylist",
			desc:  "Download today's vendor quotes and rank the disagreements.",
			where: func(m *Model) bool { return m.view == viewMarket && !m.marketLoaded },
			rank:  func(*Model) int { return 5 },
			run:   func(m *Model) tea.Cmd { return m.startMarketFetch() },
		},
		{
			id: "view.populate", title: "FetchViewData", aliases: "populate refresh load",
			desc: "Run whatever fills this view: quotes, prices, history, repairs.",
			key:  "F",
			// Leads the analytical views, where F is the verb that makes an
			// empty pane useful; on holdings the collection verbs matter more.
			rank: func(m *Model) int {
				if m.view == viewHoldings {
					return 1
				}
				return 4
			},
			run: func(m *Model) tea.Cmd { return m.populateView() },
		},
		{
			id: "watch.add", title: "WatchThisCard", aliases: "alert threshold price under over",
			desc:  "Set a price threshold on the selected card; `hoard watch` fires the alert.",
			key:   "w",
			where: func(m *Model) bool { return m.subjectCard() != nil },
			rank: func(m *Model) int {
				switch m.view {
				case viewWatches:
					return 4
				case viewMovers:
					// A mover is the card you'd want an alert on — watching
					// it outranks the everyday holdings verbs here.
					return 1
				}
				return onView(viewHoldings, 2)(m)
			},
			run: func(m *Model) tea.Cmd { m.promptWatch(); return nil },
		},
		{
			id: "watch.pick", title: "AddWatchFromCollection", aliases: "alert threshold new pick choose",
			desc: "Pick a card from your holdings and set its price alert.",
			// Watches-only: every other view can reach a card and press w
			// (or run "Watch this card") on it directly; the watches view
			// is the one place with no collection rows to point at.
			where: func(m *Model) bool { return m.view == viewWatches },
			rank:  onView(viewWatches, 5),
			run:   func(m *Model) tea.Cmd { return m.startWatchPick() },
		},
		{
			// The by-name path stays for cards you don't own yet — the
			// picker above only offers what is already held.
			id: "watch.add-by-name", title: "AddWatchForAnyCard", aliases: "alert threshold new unowned by name",
			desc: "Resolve any card by name, owned or not, and set a price alert.",
			// Watches-only, like the collection picker: everywhere else the
			// card under the cursor is the watch you'd want.
			where: func(m *Model) bool { return m.opWatchAdd != nil && m.view == viewWatches },
			// Same rank as the picker: the registry order breaks the tie, so
			// the two add-a-watch verbs sit together, picker first.
			rank: onView(viewWatches, 5),
			run:  func(m *Model) tea.Cmd { m.promptWatchByName(); return nil },
		},
		{
			id: "binder.new", title: "NewBinder", aliases: "create folder",
			desc: "Create a named binder and switch to it, ready for cards.",
			key:  "n",
			rank: onView(viewHoldings, 2),
			run:  func(m *Model) tea.Cmd { m.promptNewBinder(); return nil },
		},
		{
			id: "binder.rename", title: "RenameBinder", aliases: "name folder",
			desc: "Rename the selected binder.",
			key:  "R",
			run:  func(m *Model) tea.Cmd { m.promptRenameBinder(); return nil },
		},
		{
			id: "sets.toggle", title: "BrowseBySets",
			aliases: "sets set expansion release pane toggle",
			desc:    "Flip the left pane between binders & decks and the sets you own cards from.",
			key:     "B",
			run:     func(m *Model) tea.Cmd { m.toggleSetsMode(); return nil },
		},
		{
			id: "movers.window", aliases: "since days window",
			key: "W", hidden: true,
			where: func(m *Model) bool { return m.view == viewMovers },
			run:   func(m *Model) tea.Cmd { return m.cycleMoversWindow() },
		},
		{
			id: "movers.pennies", title: "TogglePennyFilter",
			aliases: "pennies cheap bulk noise show hide",
			desc:    "Show or hide movers priced at or under the penny limit — hidden by default.",
			where:   func(m *Model) bool { return m.view == viewMovers },
			rank:    onView(viewMovers, 1),
			run: func(m *Model) tea.Cmd {
				m.moversPennies = !m.moversPennies
				m.deriveView()
				state := "on"
				if m.moversPennies {
					state = "off"
				}
				m.status, m.statusErr = fmt.Sprintf("penny filter ≤ %s %s",
					ui.Money(m.moversPennyLimit), state), false
				m.persistPennyFilters()
				return nil
			},
		},
		{
			id: "movers.pennies.limit", title: "SetPennyFilter",
			aliases: "penny limit threshold ceiling default set filter line",
			desc:    "Move the penny filter's line — movers at or under it hide.",
			where:   func(m *Model) bool { return m.view == viewMovers },
			rank:    onView(viewMovers, 1),
			run:     func(m *Model) tea.Cmd { m.promptSetPennyLimit(); return nil },
		},
		{
			id: "market.pennies", title: "TogglePennyFilter",
			aliases: "pennies cheap bulk noise floor show hide",
			desc:    "Show or hide market rows whose low ask sits under the floor — hidden by default.",
			where:   func(m *Model) bool { return m.view == viewMarket },
			rank:    onView(viewMarket, 1),
			run: func(m *Model) tea.Cmd {
				m.marketPennies = !m.marketPennies
				state := "on"
				if m.marketPennies {
					state = "off"
				}
				m.status, m.statusErr = fmt.Sprintf("penny filter < %s %s",
					ui.Money(m.marketFloor), state), false
				// After the receipt: a day-cache miss replaces it with the
				// fresh-fetch ask, which is the truer answer.
				m.refreshMarketFloor()
				m.persistPennyFilters()
				return nil
			},
		},
		{
			id: "market.pennies.limit", title: "SetPennyFilter",
			aliases: "penny limit threshold ceiling floor default set filter line",
			desc:    "Move the market floor — rows whose low ask sits under it hide.",
			where:   func(m *Model) bool { return m.view == viewMarket },
			rank:    onView(viewMarket, 1),
			run:     func(m *Model) tea.Cmd { m.promptSetMarketFloor(); return nil },
		},
		{
			id: "market.table.next", aliases: "next table section",
			key: "]", hidden: true,
			where: func(m *Model) bool { return m.view == viewMarket },
			run:   func(m *Model) tea.Cmd { m.jumpMarketSection(1); return nil },
		},
		{
			id: "market.table.prev", aliases: "previous table section",
			key: "[", hidden: true,
			where: func(m *Model) bool { return m.view == viewMarket },
			run:   func(m *Model) tea.Cmd { m.jumpMarketSection(-1); return nil },
		},
		{
			id: "page.next", aliases: "next page turn more rows",
			key: ">", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewMovers || m.view == viewHoldings
			},
			run: func(m *Model) tea.Cmd { m.turnTablePage(1); return nil },
		},
		{
			id: "page.prev", aliases: "previous page turn back",
			key: "<", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewMovers || m.view == viewHoldings
			},
			run: func(m *Model) tea.Cmd { m.turnTablePage(-1); return nil },
		},
		{
			id: "market.comps.side", aliases: "comps buy sell asks bids side",
			key: "b", hidden: true,
			where: func(m *Model) bool { return m.view == viewMarket },
			run: func(m *Model) tea.Cmd {
				m.compsBuySide = !m.compsBuySide
				// The sides sort by their own columns; an order chosen for
				// one is meaningless on the other, so the flip lands on the
				// value ranking.
				m.compsSortIdx, m.compsSortRev = 0, false
				m.sortCompRows()
				if m.compsBuySide {
					m.status, m.statusErr = "comps · buy side: the cash bid against the asks", false
				} else {
					m.status, m.statusErr = "comps · sell side: vendor sale prices", false
				}
				return nil
			},
		},
	}
}

// cycleMoversWindow advances the movers lookback and re-queries — a
// milliseconds-cheap read, exactly what the CLI's --since parameterizes.
func (m *Model) cycleMoversWindow() tea.Cmd {
	m.moversDaysIdx = (m.moversDaysIdx + 1) % len(moversWindowDays)
	m.moversPage = 0 // a new window reads from its first page
	if err := m.loadView(); err != nil {
		m.setError(err)
		return nil
	}
	m.cursor[paneCards], m.offset[paneCards] = 0, 0
	m.status = fmt.Sprintf("movers · last %d days", moversWindowDays[m.moversDaysIdx])
	m.statusErr = false
	return nil
}

// onView is a rank helper: n on one view, 0 elsewhere.
func onView(v viewMode, n int) func(*Model) int {
	return func(m *Model) int {
		if m.view == v {
			return n
		}
		return 0
	}
}

// populateView runs whatever fills the current view with fresh data — the
// per-view F key: arbitrage fetches quotes, movers refreshes prices and
// backfills history, unpriced repairs finishes, everything else refreshes
// prices. One key, and the view knows what it needs.
func (m *Model) populateView() tea.Cmd {
	switch m.view {
	case viewMarket:
		return m.startMarketFetch()
	case viewMovers:
		return m.populateMovers()
	case viewUnpriced:
		return m.populateUnpriced()
	case viewWatches:
		if err := m.loadView(); err != nil {
			m.setError(err)
			return nil
		}
		m.status, m.statusErr = "watches refreshed against stored prices", false
		return nil
	}
	return m.startOp("updating prices", m.opUpdatePrices)
}

// startBackfill runs the price-history import at one window.
func (m *Model) startBackfill(days int) tea.Cmd {
	bf := m.opBackfill
	return m.startOp("backfilling price history",
		func(ctx context.Context, p progress.Fn) (string, error) { return bf(ctx, p, days) })
}

// populateMovers is the movers pipeline: refresh today's prices, then
// backfill the history the view charts against — composed into one
// operation so a single F populates an empty view end to end.
func (m *Model) populateMovers() tea.Cmd {
	up, bf := m.opUpdatePrices, m.opBackfill
	if up == nil && bf == nil {
		m.status, m.statusErr = "price operations are unavailable in this build", true
		return nil
	}
	return m.startOp("populating price history", func(ctx context.Context, p progress.Fn) (string, error) {
		var parts []string
		if up != nil {
			s, err := up(ctx, p)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		if bf != nil {
			s, err := bf(ctx, p, 90)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " · "), nil
	})
}

// populateUnpriced is the unpriced pipeline: refresh prices first — most
// $0 rows are price gaps — then repair finishes, the other cause. Composed
// like the movers pipeline so one F attacks both.
func (m *Model) populateUnpriced() tea.Cmd {
	up, rf := m.opUpdatePrices, m.opRepairFinishes
	if up == nil && rf == nil {
		m.status, m.statusErr = "price operations are unavailable in this build", true
		return nil
	}
	return m.startOp("populating prices", func(ctx context.Context, p progress.Fn) (string, error) {
		var parts []string
		if up != nil {
			s, err := up(ctx, p)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		if rf != nil {
			s, err := rf(ctx, p)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " · "), nil
	})
}

// applies reports whether a command is available right now.
func (c command) applies(m *Model) bool { return c.where == nil || c.where(m) }

// runCommand dispatches a registry key press, reporting whether the key was
// one.
func (m *Model) runCommand(key string) (tea.Cmd, bool) {
	for i := range m.commands {
		c := &m.commands[i]
		if c.key == key && c.applies(m) {
			return c.run(m), true
		}
	}
	return nil, false
}

// showView switches the right pane to one view — the body the 'v' key has
// always had, callable with a destination so the palette can jump straight
// there.
func (m *Model) showView(v viewMode) tea.Cmd {
	if v != viewHoldings {
		// Leaving holdings abandons a watch pick in progress.
		m.watchPick = false
	}
	// Leaving arbitrage abandons any fetch still running: the answer is no
	// longer wanted, and a download that outlives the view is a surprise.
	m.cancelMarketFetch()
	m.marketLoading = false
	m.view = v
	m.focus = paneCards
	if err := m.loadView(); err != nil {
		m.setError(err)
		return nil
	}
	m.cursor[paneCards], m.offset[paneCards] = 0, 0
	// Naming the sort with the view answers the "why is this order
	// different" beat before it lands — each view keeps its own sort, and
	// arriving somewhere sorted by a column you chose last week reads as
	// wrong data until the label says otherwise. Market is the exception:
	// its four tables each keep their own sort, and naming the cursor's
	// would claim more than it says. Movers with the penny filter armed
	// names the filter too — rows silently absent read as missing data.
	m.status = "showing " + m.view.String()
	if v != viewMarket {
		m.status += " · sorted by " + m.sortLabel()
	}
	if v == viewMovers && !m.moversPennies {
		m.status += " · penny filter ≤ " + ui.Money(m.moversPennyLimit)
	}
	if v == viewMarket && !m.marketPennies {
		m.status += " · penny filter < " + ui.Money(m.marketFloor)
	}
	m.statusErr = false
	// A selection this view greys out cannot stay selected — the cursor
	// would rest on a row advertised as unselectable, over a pane whose
	// emptiness reads as "none exist anywhere". Snap to All Cards and say so.
	if !m.containerEligible(m.cursor[paneContainers]) {
		sel := m.selectedContainer()
		m.cursor[paneContainers], m.offset[paneContainers] = 0, 0
		if err := m.loadCards(); err != nil {
			m.setError(err)
			return nil
		}
		m.deriveView()
		if sel != nil {
			m.status = fmt.Sprintf("%s has no %s · showing all cards", sel.Name, m.view.String())
		}
	}
	if v == viewMarket {
		// An earlier session's quotes beat an empty pane; with nothing
		// cached at all the fetch starts itself — an empty table inviting a
		// keypress is a chore, not a choice (owner's call). Refreshing
		// data that already exists stays deliberate: that is what F is for.
		m.loadCachedMarket()
		if !m.marketLoaded && !m.marketLoading && m.marketFetch != nil {
			return m.startMarketFetch()
		}
	}
	return nil
}
