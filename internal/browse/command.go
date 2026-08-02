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
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

type command struct {
	id string
	// title is what the palette shows and fuzzy-matches; a trailing "…"
	// marks a command that will ask for more (a prompt or a confirm).
	title string
	// aliases are extra fuzzy fodder, never displayed.
	aliases string
	// key is the direct binding, shown as the palette's hint column; ""
	// means palette-only.
	key string
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
			id: "add", title: "Add cards", aliases: "scan camera new card",
			key:  "a",
			rank: onView(viewHoldings, 1),
			run: func(m *Model) tea.Cmd {
				if m.op != nil {
					// The add cascade hands the terminal to another program;
					// quitting now would strand the op's goroutine writing
					// into a dead one.
					m.status = "wait for " + m.op.title + " to finish (esc cancels it)"
					m.statusErr = true
					return nil
				}
				// Quit with the flag set; Run's caller runs the cascade and
				// re-enters.
				m.wantAdd = true
				return tea.Quit
			},
		},
		{
			id: "remove", title: "Remove selected…", aliases: "delete card deck",
			key: "d",
			run: func(m *Model) tea.Cmd { m.askRemoval(); return nil },
		},
		{
			// No where guard: an empty undo stack answers "nothing to undo"
			// through run — the feedback is the point, and hiding the command
			// would swallow it.
			id: "undo", title: "Undo last edit", aliases: "revert restore",
			key: "u",
			run: func(m *Model) tea.Cmd { m.undoRecorded(); return nil },
		},
		{
			id: "sort", title: "Sort: next column", aliases: "order by",
			key: "s",
			run: func(m *Model) tea.Cmd {
				m.cycleSort()
				m.cursor[paneCards], m.offset[paneCards] = 0, 0
				m.status, m.statusErr = "sorted by "+m.sortLabel(), false
				return nil
			},
		},
		{
			id: "sort.reverse", title: "Sort: reverse", aliases: "order descending ascending",
			key: "S",
			run: func(m *Model) tea.Cmd {
				m.reverseSort()
				m.cursor[paneCards], m.offset[paneCards] = 0, 0
				m.status, m.statusErr = "sorted by "+m.sortLabel(), false
				return nil
			},
		},
		{
			id: "reload", title: "Reload from the database", aliases: "refresh",
			key: "r",
			run: func(m *Model) tea.Cmd { m.reload(); return nil },
		},
		{
			id: "view.next", title: "Next view", aliases: "cycle switch",
			key: "v",
			run: func(m *Model) tea.Cmd { return m.showView(m.view.next()) },
		},
		{
			id: "view.holdings", title: "View: holdings", aliases: "cards binder",
			run: func(m *Model) tea.Cmd { return m.showView(viewHoldings) },
		},
		{
			id: "view.movers", title: "View: movers", aliases: "risers sinkers prices changed",
			run: func(m *Model) tea.Cmd { return m.showView(viewMovers) },
		},
		{
			id: "view.unpriced", title: "View: unpriced", aliases: "zero gaps missing",
			run: func(m *Model) tea.Cmd { return m.showView(viewUnpriced) },
		},
		{
			id: "view.arbitrage", title: "View: arbitrage", aliases: "vendors spread buylist",
			run: func(m *Model) tea.Cmd { return m.showView(viewArbitrage) },
		},
		{
			id: "op.update-prices", title: "Update prices", aliases: "refresh fetch scryfall daily",
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
			id: "op.backfill", title: "Backfill 90 days of price history (~150 MB)",
			aliases: "backdate movers history mtgjson import past",
			where:   func(m *Model) bool { return m.opBackfill != nil },
			rank: func(m *Model) int {
				if m.view == viewMovers {
					if len(m.movers) == 0 {
						return 5
					}
					return 3
				}
				return 0
			},
			run: func(m *Model) tea.Cmd { return m.startOp("backfilling price history", m.opBackfill) },
		},
		{
			id: "op.repair-finishes", title: "Repair finishes", aliases: "fix foil unpriced zero",
			key:   "f",
			where: func(m *Model) bool { return m.opRepairFinishes != nil },
			rank:  onView(viewUnpriced, 4),
			run:   func(m *Model) tea.Cmd { return m.startOp("repairing finishes", m.opRepairFinishes) },
		},
		{
			id: "op.catalog-update", title: "Update the card catalog", aliases: "download bundle scryfall rebuild",
			where: func(m *Model) bool { return m.opCatalogUpdate != nil },
			run:   func(m *Model) tea.Cmd { return m.startOp("updating the catalog", m.opCatalogUpdate) },
		},
		{
			id: "op.cancel", title: "Cancel the running operation", aliases: "stop abort",
			where: func(m *Model) bool { return m.op != nil },
			rank:  func(*Model) int { return 10 },
			run:   func(m *Model) tea.Cmd { m.cancelOp(); return nil },
		},
		{
			id: "arb.fetch", title: "Fetch vendor prices", aliases: "arbitrage quotes compare",
			where: func(m *Model) bool { return m.view == viewArbitrage && !m.arbLoaded },
			rank:  func(*Model) int { return 5 },
			run:   func(m *Model) tea.Cmd { return m.startArbitrage() },
		},
		{
			id: "view.populate", title: "Fetch this view's data", aliases: "populate refresh load",
			key:  "F",
			rank: func(*Model) int { return 4 },
			run:  func(m *Model) tea.Cmd { return m.populateView() },
		},
		{
			id: "watch.add", title: "Watch this card…", aliases: "alert threshold price under over",
			key:   "w",
			where: func(m *Model) bool { return m.subjectCard() != nil },
			rank: func(m *Model) int {
				if m.view == viewWatches {
					return 4
				}
				return onView(viewHoldings, 2)(m)
			},
			run: func(m *Model) tea.Cmd { m.promptWatch(); return nil },
		},
		{
			id: "watch.add-by-name", title: "Add a watch by name…", aliases: "alert threshold new card",
			where: func(m *Model) bool { return m.opWatchAdd != nil },
			rank:  onView(viewWatches, 5),
			run:   func(m *Model) tea.Cmd { m.promptWatchByName(); return nil },
		},
		{
			id: "binder.new", title: "New binder…", aliases: "create folder",
			key: "n",
			run: func(m *Model) tea.Cmd { m.promptNewBinder(); return nil },
		},
		{
			id: "binder.rename", title: "Rename binder…", aliases: "name folder",
			key: "R",
			run: func(m *Model) tea.Cmd { m.promptRenameBinder(); return nil },
		},
		{
			id: "movers.window", title: "Movers: cycle the window", aliases: "since days lookback",
			key:   "W",
			where: func(m *Model) bool { return m.view == viewMovers },
			rank:  func(*Model) int { return 2 },
			run:   func(m *Model) tea.Cmd { return m.cycleMoversWindow() },
		},
		{
			id: "movers.7", title: "Movers: last 7 days", aliases: "week window since",
			run: func(m *Model) tea.Cmd { return m.jumpMovers(7) },
		},
		{
			id: "movers.30", title: "Movers: last 30 days", aliases: "month window since",
			run: func(m *Model) tea.Cmd { return m.jumpMovers(30) },
		},
		{
			id: "movers.90", title: "Movers: last 90 days", aliases: "quarter window since",
			run: func(m *Model) tea.Cmd { return m.jumpMovers(90) },
		},
	}
}

// cycleMoversWindow advances the movers lookback and re-queries — a
// milliseconds-cheap read, exactly what the CLI's --since parameterizes.
func (m *Model) cycleMoversWindow() tea.Cmd {
	m.moversDaysIdx = (m.moversDaysIdx + 1) % len(moversWindowDays)
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
	case viewArbitrage:
		return m.startArbitrage()
	case viewMovers:
		return m.populateMovers()
	case viewUnpriced:
		return m.startOp("repairing finishes", m.opRepairFinishes)
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
			s, err := bf(ctx, p)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " · "), nil
	})
}

// jumpMovers goes straight to the movers view at one lookback.
func (m *Model) jumpMovers(days int) tea.Cmd {
	for i, d := range moversWindowDays {
		if d == days {
			m.moversDaysIdx = i
		}
	}
	cmd := m.showView(viewMovers)
	m.status = fmt.Sprintf("movers · last %d days", days)
	return cmd
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
	// Leaving arbitrage abandons any fetch still running: the answer is no
	// longer wanted, and a download that outlives the view is a surprise.
	m.cancelArbitrage()
	m.arbLoading = false
	m.view = v
	m.focus = paneCards
	if err := m.loadView(); err != nil {
		m.setError(err)
		return nil
	}
	m.cursor[paneCards], m.offset[paneCards] = 0, 0
	m.status = "showing " + m.view.String()
	m.statusErr = false
	return nil
}
