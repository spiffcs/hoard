package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type CardAdd struct {
	ContainerID int64
	Binder      string
	Card        scryfall.Card
	Finish      finish.Finish

	Condition string
	Quantity  int

	PurchasePrice *float64
}

const entryAccumulateSQL = `
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, purchase_price, quantity)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board, COALESCE(purchase_price, -1))
DO UPDATE SET quantity = quantity + excluded.quantity`

type ImportReceipt struct {
	Hash  string
	File  string
	Cards int
}

func (s *Store) RecordReceipt(r ImportReceipt) error {
	_, err := s.db.Exec(`
INSERT INTO import_ledger (hash, file, cards, imported_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO UPDATE SET file=excluded.file, cards=excluded.cards,
    imported_at=excluded.imported_at`,
		r.Hash, r.File, r.Cards, now())
	if err != nil {
		return fmt.Errorf("recording receipt: %w", err)
	}
	return nil
}

func (s *Store) ImportedAt(hash string) (when string, cards int, ok bool, err error) {
	err = s.db.QueryRow(`SELECT imported_at, cards FROM import_ledger WHERE hash=?`, hash).
		Scan(&when, &cards)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("reading import ledger: %w", err)
	}
	return when, cards, true, nil
}

func (s *Store) ApplyImport(receipt *ImportReceipt, newBinders []string, adds []CardAdd) (map[string]int64, error) {

	type binderPlan struct{ name, sourceID string }
	plans := make([]binderPlan, 0, len(newBinders))
	for _, name := range newBinders {
		trimmed, sid, err := s.validateNewBinderName(name)
		if err != nil {
			return nil, err
		}
		plans = append(plans, binderPlan{trimmed, sid})
	}
	for _, a := range adds {
		if err := validFinish(a.Finish); err != nil {
			return nil, err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	created := make(map[string]int64, len(plans))
	for _, p := range plans {
		res, err := tx.Exec(insertBinderSQL, KindCollection, p.name, p.sourceID, now(), now())
		if err != nil {
			return nil, fmt.Errorf("creating binder %q: %w", p.name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		created[p.name] = id
	}

	cards := make([]scryfall.Card, len(adds))
	for i, a := range adds {
		cards[i] = a.Card
	}
	if err := upsertPrintingsTx(tx, cards); err != nil {
		return nil, err
	}

	stmt, err := tx.Prepare(entryAccumulateSQL)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, a := range adds {
		cid := a.ContainerID
		if cid == 0 {
			var ok bool
			if cid, ok = created[a.Binder]; !ok {
				return nil, fmt.Errorf("add for %q names binder %q, which this import does not create", a.Card.Name, a.Binder)
			}
		}

		if _, err := stmt.Exec(cid, a.Card.ID, a.Finish, orUnknown(a.Condition),
			"main", a.PurchasePrice, a.Quantity); err != nil {
			return nil, fmt.Errorf("adding %s: %w", a.Card.Name, err)
		}
	}

	if receipt != nil {
		if _, err := tx.Exec(`
INSERT INTO import_ledger (hash, file, cards, imported_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO UPDATE SET file=excluded.file, cards=excluded.cards,
    imported_at=excluded.imported_at`,
			receipt.Hash, receipt.File, receipt.Cards, now()); err != nil {
			return nil, fmt.Errorf("recording import receipt: %w", err)
		}
	}
	return created, tx.Commit()
}
