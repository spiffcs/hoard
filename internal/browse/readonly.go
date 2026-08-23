package browse

import (
	"errors"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

var errReadOnly = errors.New(
	"this is a catalog database, not your collection; it is read-only")

type readOnly struct{ Store }

func ReadOnly(st Store) Store { return readOnly{st} }

func (readOnly) AddWatch(string, string, finish.Finish, string, float64) error { return errReadOnly }

func (readOnly) RemoveWatch(int64) error { return errReadOnly }

func (readOnly) CreateBinder(string) (int64, error) { return 0, errReadOnly }

func (readOnly) RenameBinder(int64, string) error { return errReadOnly }

func (readOnly) DeleteBinder(int64) error { return errReadOnly }

func (readOnly) SetHoldingQuantityIn(int64, string, finish.Finish, string, int) (int, error) {
	return 0, errReadOnly
}

func (readOnly) RemoveFromBinder(int64, string) ([]store.Holding, error) { return nil, errReadOnly }

func (readOnly) RestoreHoldings(string, []store.Holding) error { return errReadOnly }

func (readOnly) RemoveContainer(int64) (int64, error) { return 0, errReadOnly }

func (readOnly) UpsertDeck(store.DeckMeta, []store.Entry) (int64, error) { return 0, errReadOnly }

func (readOnly) MoveEntry(int64, string, finish.Finish, string, int64, string) (int, error) {
	return 0, errReadOnly
}

func (readOnly) MoveEntryFinish(int64, string, finish.Finish, finish.Finish, string) (int, error) {
	return 0, errReadOnly
}

func (readOnly) MoveEntryCondition(int64, string, finish.Finish, string, string) (int, error) {
	return 0, errReadOnly
}

func (readOnly) UpsertPrintings([]scryfall.Card) error { return errReadOnly }

func WithReadOnly() Option {
	return func(m *Model) {
		m.store = ReadOnly(m.store)

		m.opUpdatePrices = nil
		m.opCorrectPrices = nil
		m.opRepairFinishes = nil
		m.opBackfill = nil
		m.opWatchAdd = nil
		m.opWatchImport = nil
		m.opDeckAdd = nil
		m.opDeckAddFile = nil
		m.opImport = nil
		m.newAddChild = nil
	}
}
