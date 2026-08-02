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

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
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
