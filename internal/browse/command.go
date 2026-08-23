package browse

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type command struct {
	id string

	title string

	aliases string

	desc string

	key string

	hidden bool

	hide func(*Model) bool

	where func(*Model) bool

	rank func(*Model) int

	run func(*Model) tea.Cmd
}

func commands() []command {
	return []command{
		{
			id: "add", title: "AddCards", aliases: "scan camera new card",
			desc: "Open the add flow: type a card name, or scan with your iPhone.",
			key:  "a",
			rank: onView(viewHoldings, 3),

			run: func(m *Model) tea.Cmd { return m.openAddCascade() },
		},
		{
			id: "remove", title: "RemoveSelected", aliases: "delete card deck",
			desc: "Remove the selected card or deck, after a y/n confirm.",
			key:  "d",

			hide: func(m *Model) bool { return m.view == viewWatches || m.view == viewMarket },
			run:  func(m *Model) tea.Cmd { m.askRemoval(); return nil },
		},
		{

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
				if singleTableView(m.view) {

					m.cardsPage, m.moversPage = 0, 0
				}
				m.cycleSort()
				if singleTableView(m.view) {
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
				if singleTableView(m.view) {
					m.cardsPage, m.moversPage = 0, 0
				}
				m.reverseSort()
				if singleTableView(m.view) {
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
				case m.view == viewMovers, m.view == viewWatches:
					return 3
				}
				return 2
			},
			run: func(m *Model) tea.Cmd { return m.startPriceOp("updating prices", m.opUpdatePrices) },
		},
		{
			id: "op.backfill", title: "BackfillPriceHistory30",
			aliases: "backdate movers history mtgjson import past",

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

			hide: func(m *Model) bool { return m.detail == nil && m.view == viewMovers },
			rank: onView(viewWatches, 4),
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
			id: "deck.add-file", title: "AddDeckFromFile", aliases: "import moxfield decklist text export file",
			desc:  "Import a decklist you exported to a file as a deck.",
			where: func(m *Model) bool { return m.opDeckAddFile != nil },
			rank:  onView(viewHoldings, 2),
			run:   func(m *Model) tea.Cmd { m.promptDeckFile(); return nil },
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

				if sel.Kind == kindAllCards {
					m.promptExport("", "", "hoard-export")
					return nil
				}

				if sel.Kind == kindSet {
					m.status, m.statusErr = "sets can't be exported directly · ExportEverything writes the whole hoard", true
					return nil
				}

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

					return 1
				}
				return onView(viewHoldings, 2)(m)
			},
			run: func(m *Model) tea.Cmd { m.promptWatch(); return nil },
		},
		{
			id: "watch.pick", title: "AddWatchFromCollection", aliases: "alert threshold new pick choose",
			desc: "Pick a card from your holdings and set its price alert.",

			where: func(m *Model) bool { return m.view == viewWatches },
			rank:  onView(viewWatches, 5),
			run:   func(m *Model) tea.Cmd { return m.startWatchPick() },
		},
		{

			id: "watch.add-by-name", title: "AddWatchForAnyCard", aliases: "alert threshold new unowned by name",
			desc: "Resolve any card by name, owned or not, and set a price alert.",

			where: func(m *Model) bool { return m.opWatchAdd != nil && m.view == viewWatches },

			rank: onView(viewWatches, 5),
			run:  func(m *Model) tea.Cmd { m.promptWatchByName(); return nil },
		},
		{
			id: "watch.import", title: "ImportWatchList", aliases: "bulk file csv json alert thresholds",
			desc: "Import price watches in bulk from a CSV or JSON file.",

			where: func(m *Model) bool { return m.opWatchImport != nil && m.view == viewWatches },
			rank:  onView(viewWatches, 5),
			run:   func(m *Model) tea.Cmd { m.promptWatchImportPath(); return nil },
		},
		{
			id: "folder.new", title: "NewFolder", aliases: "folder group decks create",
			desc:  "Create a folder to group decks under.",
			where: func(m *Model) bool { return m.view == viewHoldings && !m.setsMode },
			rank:  onView(viewHoldings, 2),
			run:   func(m *Model) tea.Cmd { m.promptNewFolder(); return nil },
		},
		{
			id: "deck.move", title: "MoveDeckToFolder", aliases: "group file organise organize",
			desc:  "Put the selected deck in a folder, or move it back to the top level.",
			key:   "m",
			where: func(m *Model) bool { return m.view == viewHoldings && !m.setsMode },
			rank:  onView(viewHoldings, 2),
			run:   func(m *Model) tea.Cmd { m.promptMoveDeck(); return nil },
		},
		{
			id: "binder.new", title: "NewBinder", aliases: "create labelled part",
			desc: "Create a named binder and switch to it, ready for cards.",
			key:  "n",
			rank: onView(viewHoldings, 2),
			run:  func(m *Model) tea.Cmd { m.promptNewBinder(); return nil },
		},
		{
			id: "binder.rename", title: "RenameBinder", aliases: "name label",
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
			desc:    "Show or hide movers priced at or under the penny limit. Hidden by default.",
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
			desc:    "Move the penny filter's line. Movers at or under it hide.",
			where:   func(m *Model) bool { return m.view == viewMovers },
			rank:    onView(viewMovers, 1),
			run:     func(m *Model) tea.Cmd { m.promptSetPennyLimit(); return nil },
		},
		{

			id: "movers.settling", title: "SetSettlingWindow",
			aliases: "settling new set release days window hold out net exclude preorder",
			desc:    "Days a new set is held out of the movers net. 0 counts every set.",
			where:   func(m *Model) bool { return m.view == viewMovers || m.setsMode },
			rank:    onView(viewMovers, 1),
			run:     func(m *Model) tea.Cmd { m.promptSetSettlingWindow(); return nil },
		},
		{
			id: "market.pennies", title: "TogglePennyFilter",
			aliases: "pennies cheap bulk noise floor show hide",
			desc:    "Show or hide market rows whose low ask sits under the floor. Hidden by default.",
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

				m.refreshMarketFloor()
				m.persistPennyFilters()
				return nil
			},
		},
		{
			id: "market.pennies.limit", title: "SetPennyFilter",
			aliases: "penny limit threshold ceiling floor default set filter line",
			desc:    "Move the market floor. Rows whose low ask sits under it hide.",
			where:   func(m *Model) bool { return m.view == viewMarket },
			rank:    onView(viewMarket, 1),
			run:     func(m *Model) tea.Cmd { m.promptSetMarketFloor(); return nil },
		},
		{

			id: "table.next", aliases: "next table section",
			key: "]", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewWatches || m.view == viewDip
			},
			run: func(m *Model) tea.Cmd { m.jumpSection(1); return nil },
		},
		{
			id: "table.prev", aliases: "previous table section",
			key: "[", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewWatches || m.view == viewDip
			},
			run: func(m *Model) tea.Cmd { m.jumpSection(-1); return nil },
		},
		{
			id: "page.next", aliases: "next page turn more rows",
			key: ">", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewMovers ||
					m.view == viewHoldings || m.view == viewDip
			},
			run: func(m *Model) tea.Cmd { m.turnTablePage(1); return nil },
		},
		{
			id: "page.prev", aliases: "previous page turn back",
			key: "<", hidden: true,
			where: func(m *Model) bool {
				return m.view == viewMarket || m.view == viewMovers ||
					m.view == viewHoldings || m.view == viewDip
			},
			run: func(m *Model) tea.Cmd { m.turnTablePage(-1); return nil },
		},
		{

			id: "market.buylist.band", aliases: "buylist lowball band good bad scam pays side",
			key: "b", hidden: true,
			where: func(m *Model) bool {
				if m.view != viewMarket {
					return false
				}
				sec, _ := m.marketCursorPos()
				return sec == int(market.KindLiquid)
			},
			run: func(m *Model) tea.Cmd {
				m.liquidLowball = !m.liquidLowball

				m.marketSortIdx[market.KindLiquid] = 0
				m.marketSortRev[market.KindLiquid] = m.liquidLowball

				m.applyMarketRows()

				m.cursor[paneCards] = m.marketSections()[market.KindLiquid].curStart
				m.scrollIntoView()
				if m.liquidLowball {
					m.status, m.statusErr = "buylist · lowball band: who is paying under half of market", false
				} else {
					m.status, m.statusErr = "buylist · near-market band: who is paying close to market", false
				}
				return nil
			},
		},
		{
			id: "market.comps.side", aliases: "comps buy sell asks bids side",
			key: "b", hidden: true,

			where: func(m *Model) bool { return m.view == viewMarket && m.selectedComp() != nil },
			run: func(m *Model) tea.Cmd {
				m.compsBuySide = !m.compsBuySide

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

func (m *Model) cycleMoversWindow() tea.Cmd {
	m.moversDaysIdx = (m.moversDaysIdx + 1) % len(moversWindowDays)
	m.moversPage = 0
	if err := m.loadView(); err != nil {
		m.setError(err)
		return nil
	}
	m.cursor[paneCards], m.offset[paneCards] = 0, 0
	m.status = fmt.Sprintf("movers · last %d days", moversWindowDays[m.moversDaysIdx])
	m.statusErr = false
	return nil
}

func singleTableView(v viewMode) bool {
	return v != viewMarket && v != viewWatches
}

func (m *Model) jumpSection(dir int) {
	switch m.view {
	case viewWatches:
		m.jumpWatchSection(dir)
	case viewDip:
		m.jumpDipSection(dir)
	default:
		m.jumpMarketSection(dir)
	}
}

func onView(v viewMode, n int) func(*Model) int {
	return func(m *Model) int {
		if m.view == v {
			return n
		}
		return 0
	}
}

func (m *Model) populateView() tea.Cmd {
	switch m.view {
	case viewMarket:
		return m.startMarketFetch()
	case viewMovers:
		return m.populateMovers()
	case viewWatches:
		return m.populateWatches()
	}
	return m.startPriceOp("updating prices", m.opUpdatePrices)
}

func (m *Model) startBackfill(days int) tea.Cmd {
	bf := m.opBackfill
	return m.startOp("backfilling price history",
		func(ctx context.Context, p progress.Fn) (string, error) { return bf(ctx, p, days) })
}

func (m *Model) populateMovers() tea.Cmd {
	up, bf := m.opUpdatePrices, m.opBackfill
	if up == nil && bf == nil {
		m.status, m.statusErr = "price operations are unavailable in this build", true
		return nil
	}
	return m.startPriceOp("populating price history", func(ctx context.Context, p progress.Fn) (string, error) {
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

func (m *Model) populateWatches() tea.Cmd {
	up, rf := m.opUpdatePrices, m.opRepairFinishes
	if up == nil && rf == nil {
		m.status, m.statusErr = "price operations are unavailable in this build", true
		return nil
	}
	return m.startPriceOp("populating prices", func(ctx context.Context, p progress.Fn) (string, error) {
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

func (c command) applies(m *Model) bool { return c.where == nil || c.where(m) }

func (m *Model) runCommand(key string) (tea.Cmd, bool) {
	for i := range m.commands {
		c := &m.commands[i]
		if c.key == key && c.applies(m) {
			return c.run(m), true
		}
	}
	return nil, false
}

func (m *Model) showView(v viewMode) tea.Cmd {
	if v != viewHoldings {

		m.watchPick = false
	}

	m.cancelMarketFetch()
	m.marketLoading = false
	m.view = v
	m.focus = paneCards
	if err := m.loadView(); err != nil {
		m.setError(err)
		return nil
	}
	m.cursor[paneCards], m.offset[paneCards] = 0, 0
	if v == viewWatches {

		m.watchSecOffset = [watchSectionCount]int{}
		m.cursor[paneCards] = m.firstWatchCursor()
	}
	if v == viewDip {

		m.dipSecOffset = [2]int{}
		m.cursor[paneCards] = m.firstDipCursor()
	}

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

	if err := m.restorePick(); err != nil {
		m.setError(err)
		return nil
	}
	if v == viewMarket {

		m.loadCachedMarket()
		if !m.marketLoaded && !m.marketLoading && m.marketFetch != nil {
			return m.startMarketFetch()
		}
	}
	return nil
}
