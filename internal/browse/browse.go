package browse

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

type Editor interface {
	AddWatch(scryfallID, display string, fin finish.Finish, op string, threshold float64) error
	RemoveWatch(id int64) error
	CreateBinder(name string) (int64, error)
	RenameBinder(id int64, name string) error
	DeleteBinder(id int64) error

	SetHoldingQuantityIn(containerID int64, scryfallID string, fin finish.Finish, condition string, qty int) (int, error)
	RemoveFromBinder(containerID int64, scryfallID string) ([]store.Holding, error)
	RestoreHoldings(scryfallID string, holdings []store.Holding) error
	RemoveContainer(id int64) (int64, error)
	UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error)

	MoveEntry(fromContainer int64, scryfallID string, fin finish.Finish, condition string, toContainer int64, toScryfallID string) (int, error)
	MoveEntryFinish(containerID int64, scryfallID string, fromFinish, toFinish finish.Finish, condition string) (int, error)
	MoveEntryCondition(containerID int64, scryfallID string, fin finish.Finish, fromCondition, toCondition string) (int, error)
	UpsertPrintings(cards []scryfall.Card) error
}

type Store interface {
	Editor

	ListBinders() ([]store.DeckSummary, error)
	ListDecks() ([]store.DeckSummary, error)
	BinderByFinish(containerID int64) ([]store.CollectionRow, error)
	DeckEntries(containerID int64) ([]store.EntryView, error)

	MatchingCardIDs(f store.TraitFilter) (map[string]bool, error)
	EnrichedCount() (enriched, total int, err error)

	Movers(since string) ([]store.PriceChange, error)
	Unpriced() ([]store.UnpricedRow, error)

	EntryKeys() ([]store.EntryKey, error)

	CardDetail(scryfallID string) (store.CardDetail, error)
	HoldingsOf(scryfallID string) ([]store.Holding, error)
	HoldingsOfName(name string) ([]store.Holding, error)
	PriceSeries(scryfallID string, fin finish.Finish) ([]store.PricePoint, error)
	BidSeries(scryfallID string, fin finish.Finish) ([]store.PricePoint, error)

	AllByFinish() ([]store.CollectionRow, error)

	SetsHeld() ([]store.SetSummary, error)
	SetByFinish(setCode string) ([]store.CollectionRow, error)

	ListWatches() ([]store.WatchStatus, error)
	WouldFire() ([]store.WatchStatus, error)

	Settings() (map[string]string, error)
	SaveSettings(map[string]string) error

	DataVersion() (int64, error)
}

type Option func(*Model)

func WithEnv(e ui.Env) Option {
	return func(m *Model) { m.env = e }
}

func WithMarket(f MarketFunc) Option {
	return func(m *Model) { m.marketFetch = f }
}

func WithMarketCached(f MarketCachedFunc) Option {
	return func(m *Model) { m.marketCached = f }
}

type CardCompFunc func(scryfallID string) (map[finish.Finish]market.Comp, bool)

func WithCardComps(f CardCompFunc) Option {
	return func(m *Model) { m.cardComps = f }
}

type OpenURLFunc func(url string) error

func WithOpenURL(f OpenURLFunc) Option {
	return func(m *Model) { m.openURL = f }
}

type PrintSearchFunc func(ctx context.Context, exactName string) ([]scryfall.Card, error)

func WithPrintSearch(f PrintSearchFunc) Option {
	return func(m *Model) { m.printSearch = f }
}

type ReportFunc func(top, width int) ([]string, error)

func WithReport(f ReportFunc) Option {
	return func(m *Model) { m.reportFn = f }
}

type ExportFunc func(binderRef, deckRef, format, path string) (summary string, err error)

func WithExport(f ExportFunc) Option {
	return func(m *Model) { m.exportFn = f }
}

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

	fm.teardownAddChild()
	return fm.addSummary, fm.err
}
