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
	tea "github.com/charmbracelet/bubbletea"
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
	// run performs the command. It may stage a confirm or a prompt.
	run func(*Model) tea.Cmd
}

// commands is the registry, in palette display order: the everyday verbs
// first, view jumps after.
func commands() []command {
	return []command{
		{
			id: "add", title: "Add cards", aliases: "scan camera new card",
			key: "a",
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
			run:   func(m *Model) tea.Cmd { return m.startOp("updating prices", m.opUpdatePrices) },
		},
		{
			id: "op.repair-finishes", title: "Repair finishes", aliases: "fix foil unpriced zero",
			key:   "f",
			where: func(m *Model) bool { return m.opRepairFinishes != nil },
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
			run:   func(m *Model) tea.Cmd { m.cancelOp(); return nil },
		},
	}
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
