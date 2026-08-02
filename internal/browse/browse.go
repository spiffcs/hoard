// Package browse provides the interactive view of a hoard: containers on the
// left, the cards inside the selected one on the right.
//
// It replaces four one-shot tables — list, summary, deck list and deck show —
// with one screen. The left pane is the summary; selecting a row is deck show.
// Comparing a deck against the loose collection stops being two commands and a
// good memory.
//
// This is a separate package from internal/tui, which is the add cascade: that
// one is a wizard driven by Scryfall lookups, this one is a reader driven by
// the local database, and they share no state beyond the store.
package browse

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

// Editor changes what is held; it is split out of Store because the undo
// closures take an Editor, not the whole database. UpsertDeck is here to undo
// a deck removal: RemoveContainer cascades, so the deck is rebuilt from what
// was read before the delete.
type Editor interface {
	// Watches and binders join the edit surface with the parity sprint;
	// binder methods are store-direct by design (the undo closures need
	// them).
	AddWatch(scryfallID, display, finish, op string, threshold float64) error
	RemoveWatch(id int64) error
	CreateBinder(name string) (int64, error)
	RenameBinder(id int64, name string) error
	DeleteBinder(id int64) error

	SetHoldingQuantityIn(containerID int64, scryfallID, finish string, qty int) (int, error)
	RemoveFromBinder(containerID int64, scryfallID string) ([]store.Holding, error)
	RestoreHoldings(scryfallID string, holdings []store.Holding) error
	RemoveContainer(id int64) (int64, error)
	UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error)
}

// Store is everything the browser reads and edits. *store.Store satisfies it.
type Store interface {
	Editor

	// Filling the two panes. Binders come back default-first with their own
	// totals, so the left pane needs no synthesized rows.
	ListBinders() ([]store.DeckSummary, error)
	ListDecks() ([]store.DeckSummary, error)
	BinderByFinish(containerID int64) ([]store.CollectionRow, error)
	DeckEntries(containerID int64) ([]store.EntryView, error)

	// The half of a query that is about the printing rather than the holding.
	// EnrichedCount is what lets an empty result say "nothing has traits yet"
	// rather than "nothing matched".
	MatchingCardIDs(f store.TraitFilter) (map[string]bool, error)
	EnrichedCount() (enriched, total int, err error)

	// Hoard-wide analysis. Plain database reads; neither touches the network.
	Movers(since string) ([]store.PriceChange, error)
	Unpriced() ([]store.UnpricedRow, error)

	// One printing in depth.
	CardDetail(scryfallID string) (store.CardDetail, error)
	HoldingsOf(scryfallID string) ([]store.Holding, error)
	PriceSeries(scryfallID, finish string) ([]store.PricePoint, error)

	// The whole hoard over time, for the header sparkline.
	ValueSnapshots() ([]store.ValuePoint, error)

	// Price watches: the view's rows, and the read-only fired preview the
	// banner shows on open (never consuming the alert — cron's `hoard
	// watch` stays the consumer of record).
	ListWatches() ([]store.WatchStatus, error)
	WouldFire() ([]store.WatchStatus, error)
}

// Option configures the browser.
type Option func(*Model)

// WithArbitrage supplies the vendor-comparison fetch. Without it the arbitrage
// view says it is unavailable rather than pretending to have nothing to show.
func WithArbitrage(f ArbitrageFunc) Option {
	return func(m *Model) { m.arbitrage = f }
}

// Run opens the browser and blocks until the user leaves it.
//
// addRequested is true when the user pressed `a` rather than quitting, meaning
// the caller should run the add cascade and then call Run again. The browser
// steps aside rather than launching the cascade itself: both are bubbletea
// programs and only one can own the terminal at a time, so nesting them
// deadlocks on the input reader. Taking turns is the whole design.
func Run(ctx context.Context, st Store, opts ...Option) (addRequested bool, err error) {
	m, err := New(st, opts...)
	if err != nil {
		return false, err
	}
	m.ctx = ctx
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return false, err
	}
	fm, ok := final.(Model)
	if !ok {
		return false, nil
	}
	return fm.wantAdd, fm.err
}
