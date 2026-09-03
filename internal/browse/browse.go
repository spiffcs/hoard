package browse

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
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
	SetContainerCounted(id int64, counted bool) error
	CreateFolder(name string) (int64, error)
	MoveDeckToFolder(deckID, folderID int64) error
	RenameFolder(id int64, name string) error
	RenameDeck(id int64, name string) error

	SetEntryQuantity(ref store.EntryRef, qty int) (int, error)
	RemoveFromBinder(containerID int64, scryfallID string) ([]store.Holding, error)
	RestoreHoldings(scryfallID string, holdings []store.Holding) error
	RemoveContainer(id int64) (int64, error)
	UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error)

	MoveEntry(from store.EntryRef, toContainer int64, toScryfallID string) (int, error)
	MoveEntryFinish(from store.EntryRef, toFinish finish.Finish) (int, error)
	MoveEntryCondition(from store.EntryRef, toCondition string) (int, error)
	MoveEntryBoard(from store.EntryRef, toBoard string, copies int) (int, error)
	MoveEntryPurchasePrice(from store.EntryRef, toPaid *float64) (int, error)
	UpsertPrintings(cards []scryfall.Card) error
}

type Store interface {
	Editor

	ListBinders() ([]store.DeckSummary, error)
	ListDecks() ([]store.DeckSummary, error)
	ListFolders() ([]store.DeckSummary, error)
	FolderByFinish(folderID int64) ([]store.CollectionRow, error)
	BinderByFinish(containerID int64) ([]store.CollectionRow, error)
	DeckEntries(containerID int64) ([]store.EntryView, error)

	MatchingCardIDs(f store.TraitFilter) (map[string]bool, error)
	EnrichedCount() (enriched, total int, err error)

	Movers(since string) ([]store.PriceChange, error)
	HasCostBasis() (bool, error)
	CostBasisMovers() ([]store.PriceChange, error)
	Dips(o store.TrendOptions) ([]store.TrendRow, error)
	Momentum(o store.TrendOptions) ([]store.TrendRow, error)
	Trends(o store.TrendOptions) (dips, momentum []store.TrendRow, err error)
	Unpriced() ([]store.UnpricedRow, error)

	EntryKeys() ([]store.EntryKey, error)

	CardDetail(scryfallID string) (store.CardDetail, error)
	HoldingsOf(scryfallID string) ([]store.Holding, error)
	HoldingsOfName(name string) ([]store.Holding, error)
	PriceSeries(scryfallID string, fin finish.Finish) ([]store.PricePoint, error)
	BidSeries(scryfallID string, fin finish.Finish) ([]store.PricePoint, error)

	AllByFinish() ([]store.CollectionRow, error)
	WhereHeld() ([]store.HeldPlace, error)

	SetsHeld() ([]store.SetSummary, error)
	SetByFinish(setCode string) ([]store.CollectionRow, error)
	SetUnowned(setCode string) ([]store.UnownedRow, error)
	SetShelvedByFinish(setCode string) ([]store.UnownedRow, error)

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

type SetPrintsFunc func(ctx context.Context, setCode string) ([]scryfall.Card, error)

func WithSetPrints(f SetPrintsFunc) Option {
	return func(m *Model) { m.setPrints = f }
}

func WithPrintSearch(f PrintSearchFunc) Option {
	return func(m *Model) { m.printSearch = f }
}

type CardDocumentFunc func(ctx context.Context, scryfallID string) (scryfall.Card, error)

func WithCardDocument(f CardDocumentFunc) Option {
	return func(m *Model) { m.cardDocument = f }
}

type HistoryBackfillFunc func(ctx context.Context, scryfallID, setCode string) (int, error)

func WithHistoryBackfill(f HistoryBackfillFunc) Option {
	return func(m *Model) { m.historyBackfill = f }
}

type ReportFunc func(top, width int) ([]string, error)

func WithReport(f ReportFunc) Option {
	return func(m *Model) { m.reportFn = f }
}

type ExportFunc func(binderRef, deckRef, format, path string) (summary string, err error)

func WithExport(f ExportFunc) Option {
	return func(m *Model) { m.exportFn = f }
}

func WithCellAspect(a float64) Option {
	return func(m *Model) {
		if a > 0 {
			m.cellAspect = a
		}
	}
}

func Run(ctx context.Context, st Store, opts ...Option) (tui.Summary, error) {
	if ui.CellAspectOverride() == 0 && ui.DetectImageTier() == ui.ImageKitty {
		opts = append(opts, WithCellAspect(ui.ProbeCellAspect()))
	}
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
