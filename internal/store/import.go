package store

import (
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
	Quantity    int
}

// ApplyImport commits one file's additions atomically: the binders it
// creates, the catalog rows its cards need, and the entries themselves.
// Returns the ids of the binders it created, keyed by name.
//
// One transaction, deliberately. Entry quantities accumulate, so a
// half-committed import is indistinguishable from cards actually owned and
// cannot be safely re-run — where UpsertDeck gets its safety from replacement,
// an import gets it from atomicity. One transaction is also what makes a
// 2,000-row file cost one fsync instead of 2,000.
func (s *Store) ApplyImport(newBinders []string, adds []CardAdd) (map[string]int64, error) {
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
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
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
		if _, err := stmt.Exec(cid, a.Card.ID, a.Finish, a.Quantity); err != nil {
			return nil, fmt.Errorf("adding %s: %w", a.Card.Name, err)
		}
	}
	return created, tx.Commit()
}
