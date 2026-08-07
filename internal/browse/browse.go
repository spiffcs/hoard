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
// the local database. The cascade runs inside the browser as an embedded
// child model (see addchild.go), but its dependencies are still injected
// from outside — the two packages share types, never construction.
package browse

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
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

	SetHoldingQuantityIn(containerID int64, scryfallID, finish, condition string, qty int) (int, error)
	RemoveFromBinder(containerID int64, scryfallID string) ([]store.Holding, error)
	RestoreHoldings(scryfallID string, holdings []store.Holding) error
	RemoveContainer(id int64) (int64, error)
	UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error)

	// The detail's held-row editor: moving one row to another printing,
	// binder, or finish (merging on collision), and storing the printing
	// it moves to.
	MoveEntry(fromContainer int64, scryfallID, finish, condition string, toContainer int64, toScryfallID string) (int, error)
	MoveEntryFinish(containerID int64, scryfallID, fromFinish, toFinish, condition string) (int, error)
	MoveEntryCondition(containerID int64, scryfallID, finish, fromCondition, toCondition string) (int, error)
	UpsertPrintings(cards []scryfall.Card) error
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

	// EntryKeys feeds the membership index that lets the container pane
	// filter the analytical views; see containerfilter.go.
	EntryKeys() ([]store.EntryKey, error)

	// One printing in depth. BidSeries is the buylist twin of PriceSeries,
	// read from the bid history table.
	CardDetail(scryfallID string) (store.CardDetail, error)
	HoldingsOf(scryfallID string) ([]store.Holding, error)
	HoldingsOfName(name string) ([]store.Holding, error)
	PriceSeries(scryfallID, finish string) ([]store.PricePoint, error)
	BidSeries(scryfallID, finish string) ([]store.PricePoint, error)

	// The merged all-cards view: every holding across every container.
	AllByFinish() ([]store.CollectionRow, error)

	// Sets mode: the left pane's per-set rollup and one set's holdings.
	SetsHeld() ([]store.SetSummary, error)
	SetByFinish(setCode string) ([]store.CollectionRow, error)

	// Price watches: the view's rows, and the read-only fired preview the
	// banner shows on open (never consuming the alert — cron's `hoard
	// watch` stays the consumer of record).
	ListWatches() ([]store.WatchStatus, error)
	WouldFire() ([]store.WatchStatus, error)

	// Preferences that survive a restart — the penny filters' state; see
	// loadPennyFilters.
	Settings() (map[string]string, error)
	SaveSettings(map[string]string) error
}

// Option configures the browser.
type Option func(*Model)

// WithEnv pins the render environment instead of detecting it from stdout —
// how tests get deterministic color decisions, and how a caller could force
// color on or off.
func WithEnv(e ui.Env) Option {
	return func(m *Model) { m.env = e }
}

// WithMarket supplies the vendor-comparison fetch. Without it the arbitrage
// view says it is unavailable rather than pretending to have nothing to show.
func WithMarket(f MarketFunc) Option {
	return func(m *Model) { m.marketFetch = f }
}

// WithMarketCached supplies the no-network read of today's quote cache,
// so the view survives a restart without re-asking anyone.
func WithMarketCached(f MarketCachedFunc) Option {
	return func(m *Model) { m.marketCached = f }
}

// CardCompFunc reads today's cached vendor quotes for one printing, as
// comp sheets keyed by price finish ("nonfoil"|"foil"). ok is false when
// no fresh cache covers the holdings — the detail says how to fetch one.
type CardCompFunc func(scryfallID string) (map[string]market.Comp, bool)

// WithCardComps supplies the detail overlay's comp sheets. Without it the
// COMPS section is absent, like every other injected capability.
func WithCardComps(f CardCompFunc) Option {
	return func(m *Model) { m.cardComps = f }
}

// OpenURLFunc opens a URL in the user's web browser. Injected because
// spawning processes is not a rendering concern; without it the detail's
// LINKS section is absent.
type OpenURLFunc func(url string) error

// WithOpenURL supplies the browser opener for the detail's vendor links.
func WithOpenURL(f OpenURLFunc) Option {
	return func(m *Model) { m.openURL = f }
}

// PrintSearchFunc lists every printing of an exact card name — what the
// detail's set editor resolves a new set code against. Catalog-first in
// practice, with the Scryfall API behind it.
type PrintSearchFunc func(ctx context.Context, exactName string) ([]scryfall.Card, error)

// WithPrintSearch supplies the printing lookup. Without it the held row's
// set field refuses to edit, like every other injected capability.
func WithPrintSearch(f PrintSearchFunc) Option {
	return func(m *Model) { m.printSearch = f }
}

// ReportFunc produces the valuation report as lines already laid out for
// the given width. A fast local read — it runs synchronously in a command's
// run, not through the op layer.
type ReportFunc func(top, width int) ([]string, error)

// WithReport supplies the valuation report. Without it the palette entry is
// hidden.
func WithReport(f ReportFunc) Option {
	return func(m *Model) { m.reportFn = f }
}

// ExportFunc writes holdings to a file: refs select one container (by id)
// or, both empty, everything. A fast local write — it runs synchronously in
// the prompt commit, not through the op layer.
type ExportFunc func(binderRef, deckRef, format, path string) (summary string, err error)

// WithExport supplies the holdings export. Without it the palette entries
// are hidden.
func WithExport(f ExportFunc) Option {
	return func(m *Model) { m.exportFn = f }
}

// Run opens the browser and blocks until the user leaves it.
//
// The add cascade runs inside the browser as an embedded child model
// (WithAddCascade), so one program owns the terminal for the whole
// session. The returned Summary is the session's accumulated scan receipt
// across every cascade invocation — the caller prints it to scrollback,
// where it outlives the alternate screen.
func Run(ctx context.Context, st Store, opts ...Option) (tui.Summary, error) {
	m, err := New(st, opts...)
	if err != nil {
		return tui.Summary{}, err
	}
	m.ctx = ctx
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return tui.Summary{}, err
	}
	fm, ok := final.(Model)
	if !ok {
		return tui.Summary{}, nil
	}
	// Safety net for exits that bypass handleAddChildKey — a hard ctrl+c on
	// a confirm, or context cancellation: the camera session must not
	// outlive the program, and abandoned cards belong in the receipt.
	fm.teardownAddChild()
	return fm.addSummary, fm.err
}
