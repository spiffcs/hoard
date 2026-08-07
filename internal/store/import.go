package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// The batch write behind `hoard import`: everything one file adds, committed
// as one transaction.

// CardAdd is one import row ready to write: copies of a printing in a finish,
// destined for an existing container or a binder the same import creates.
type CardAdd struct {
	ContainerID int64  // destination id; 0 when Binder names a to-be-created one
	Binder      string // consulted only when ContainerID is 0
	Card        scryfall.Card
	Finish      string
	// Condition is the wear the source stated, or ConditionUnknown when it said
	// nothing hoard could place. Imports are the only input that carries one
	// today — a scanner cannot assess wear.
	Condition string
	Quantity  int
}

// ImportReceipt records that one file's content has been imported, so a
// re-run can be refused instead of silently doubling quantities.
type ImportReceipt struct {
	Hash  string // content hash of the file, the identity that matters
	File  string // display name for the refusal message
	Cards int
}

// RecordReceipt writes one ledger row outside an import transaction. The
// price backfill uses it to remember that today's archive has already been
// applied against the current holdings, so a same-day re-run can skip a
// 150 MB download; content-hash identity keeps the reuse honest — adding a
// card changes the hash and forces a real run.
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

// ImportedAt reports when a file with this content hash was imported, if ever.
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

// ApplyImport commits one file's additions atomically: the binders it
// creates, the catalog rows its cards need, the entries themselves, and the
// receipt saying this content has now been imported. Returns the ids of the
// binders it created, keyed by name.
//
// One transaction, deliberately. Entry quantities accumulate, so a
// half-committed import is indistinguishable from cards actually owned and
// cannot be safely re-run — where UpsertDeck gets its safety from replacement,
// an import gets it from atomicity. The receipt commits with the cards
// (exactly-once: neither exists without the other), and is upserted so an
// explicit re-import refreshes the date rather than failing. One transaction
// is also what makes a 2,000-row file cost one fsync instead of 2,000.
func (s *Store) ApplyImport(receipt *ImportReceipt, newBinders []string, adds []CardAdd) (map[string]int64, error) {
	// Vet everything vetable before the transaction opens: a name collision or
	// a bad finish should refuse the import, not abort it half-planned.
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

	stmt, err := tx.Prepare(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`)
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
			a.Quantity); err != nil {
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
