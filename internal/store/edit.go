package store

import (
	"database/sql"
	"fmt"
)

// SetHoldingQuantity sets how many copies of one printing-and-finish the binder
// holds, and reports what it held before.
//
// Per finish, not per normal/foil pair: etched is a finish of its own everywhere
// else, and a pivoted setter would leave etched entries untouched while appearing
// to have set the card's quantities.
//
// Zero deletes the entry rather than storing a zero, so "held in no copies" and
// "not held" stay one state. The previous quantity comes back so a caller can
// offer undo without a second query.
func (s *Store) SetHoldingQuantity(scryfallID, finish, condition string, qty int) (previous int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	return s.SetHoldingQuantityIn(cid, scryfallID, finish, condition, qty)
}

// SetHoldingQuantityIn is SetHoldingQuantity against a chosen binder.
//
// The condition is part of which row is addressed, not decoration: since schema
// v23 a card held near mint and lightly played is two rows, and a statement that
// named only the finish would land on whichever the query happened to return
// first — and delete both on the way to zero.
func (s *Store) SetHoldingQuantityIn(containerID int64, scryfallID, finish, condition string, qty int) (previous int, err error) {
	if err := validFinish(finish); err != nil {
		return 0, err
	}
	condition = orUnknown(condition)
	if err := validCondition(condition); err != nil {
		return 0, err
	}
	cid := containerID

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		cid, scryfallID, finish, condition).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if qty <= 0 {
		if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
			cid, scryfallID, finish, condition); err != nil {
			return 0, err
		}
		return previous, tx.Commit()
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = excluded.quantity`,
		cid, scryfallID, finish, condition, qty); err != nil {
		return 0, err
	}
	return previous, tx.Commit()
}

// MoveEntry re-points one main-board binder row at a different printing,
// a different binder, or both, merging quantities when the destination row
// already exists — two listings of one physical pile must sum. Returns the
// destination's previous quantity so the caller can offer an exact undo.
// The destination printing must already be in cards.
func (s *Store) MoveEntry(fromContainer int64, scryfallID, finish, condition string, toContainer int64, toScryfallID string) (prevTarget int, err error) {
	condition = orUnknown(condition)
	if fromContainer == toContainer && scryfallID == toScryfallID {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var qty int
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		fromContainer, scryfallID, finish, condition).Scan(&qty)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no such holding to move")
	}
	if err != nil {
		return 0, err
	}
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		toContainer, toScryfallID, finish, condition).Scan(&prevTarget)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		toContainer, toScryfallID, finish, condition, qty); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		fromContainer, scryfallID, finish, condition); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

// MoveEntryFinish re-keys a holding's finish in place — the detail's finish
// editor — merging with any copies already held in the target finish of the
// same container. Returns the merged-into quantity before the move, so an
// undo can put both sides back.
func (s *Store) MoveEntryFinish(containerID int64, scryfallID, fromFinish, toFinish, condition string) (prevTarget int, err error) {
	if fromFinish == toFinish {
		return 0, nil
	}
	// Re-keying a finish is the evidence a scanner's guess was waiting for —
	// a human looked at the card — so the newest guess standing on the old
	// finish is answered here, in the same transaction as the correction.
	// This is what makes `hoard guessed` drainable from the browser, which
	// the scan flow promises ("fix a wrong one in browse … which clears it
	// here"). Deleting nothing is fine: most re-keys never had a guess.
	//
	// The condition rides across untouched. Correcting a finish says nothing
	// about a card's wear, and resetting it would quietly discard an
	// assessment the user made.
	condition = orUnknown(condition)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var qty int
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, fromFinish, condition).Scan(&qty)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no such holding to move")
	}
	if err != nil {
		return 0, err
	}
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, toFinish, condition).Scan(&prevTarget)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		containerID, scryfallID, toFinish, condition, qty); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, fromFinish, condition); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM finish_guesses WHERE id = (
    SELECT id FROM finish_guesses
    WHERE container_id = ? AND scryfall_id = ? AND finish = ?
    ORDER BY id DESC LIMIT 1)`,
		containerID, scryfallID, fromFinish); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

// MoveCardFinish is MoveEntryFinish in the default binder — the counterpart to
// AddCardFinish, for the scan flow correcting a finish it had to guess.
func (s *Store) MoveCardFinish(scryfallID, fromFinish, toFinish string) (prevTarget int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	// The scan flow's correction only ever touches what it just added, which
	// is unassessed by construction — a camera cannot judge wear.
	return s.MoveEntryFinish(cid, scryfallID, fromFinish, toFinish, ConditionUnknown)
}

// MoveEntryCondition re-keys a holding's condition in place — the detail's
// condition editor — merging with any copies already held in that condition of
// the same container and finish. Returns the merged-into quantity before the
// move, so an undo can put both sides back.
//
// The mirror of MoveEntryFinish, and deliberately so: the two answer the same
// shape of question, and a reader who has understood one has understood both.
func (s *Store) MoveEntryCondition(containerID int64, scryfallID, finish, fromCondition, toCondition string) (prevTarget int, err error) {
	fromCondition, toCondition = orUnknown(fromCondition), orUnknown(toCondition)
	if fromCondition == toCondition {
		return 0, nil
	}
	if err := validCondition(toCondition); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var qty int
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, finish, fromCondition).Scan(&qty)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no such holding to move")
	}
	if err != nil {
		return 0, err
	}
	err = tx.QueryRow(`
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, finish, toCondition).Scan(&prevTarget)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		containerID, scryfallID, finish, toCondition, qty); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, finish, fromCondition); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

// RemoveFromCollection drops every entry for a printing from the loose
// collection and returns what it removed.
//
// Returning the rows is what makes the removal undoable: a card can be held in
// several finishes at once, so "how many were there" is a list, not a number,
// and re-adding one copy of one finish would not restore it.
//
// Decks are untouched. A card in a deck is held by that deck, and removing it
// from the collection must not quietly empty a decklist that was imported from
// somewhere else.
func (s *Store) RemoveFromCollection(scryfallID string) ([]Holding, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	return s.RemoveFromBinder(cid, scryfallID)
}

// RemoveFromBinder is RemoveFromCollection against a chosen binder.
func (s *Store) RemoveFromBinder(containerID int64, scryfallID string) ([]Holding, error) {
	cid := containerID

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// condition rides the payload: without it an undo restores every condition
	// bucket of this card as one unassessed row, silently merging what the user
	// had told hoard apart.
	rows, err := tx.Query(`
SELECT finish, condition, board, quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ?`, cid, scryfallID)
	if err != nil {
		return nil, fmt.Errorf("reading entries for %s: %w", scryfallID, err)
	}
	var removed []Holding
	for rows.Next() {
		h := Holding{ContainerID: cid, ContainerKind: KindCollection}
		if err := rows.Scan(&h.Finish, &h.Condition, &h.Board, &h.Quantity); err != nil {
			rows.Close()
			return nil, err
		}
		removed = append(removed, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if _, err := tx.Exec(`
DELETE FROM card_entries WHERE container_id = ? AND scryfall_id = ?`,
		cid, scryfallID); err != nil {
		return nil, fmt.Errorf("removing %s from the collection: %w", scryfallID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return removed, nil
}

// RestoreHoldings puts back entries a removal returned, undoing it.
//
// Rows are written back to the container they came from, so this also undoes a
// deck removal once the container itself has been recreated. Quantities are
// replaced rather than added: undo restores the state that was there, and
// adding would double a holding that was partly re-added in between.
func (s *Store) RestoreHoldings(scryfallID string, holdings []Holding) error {
	if len(holdings) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = excluded.quantity`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, h := range holdings {
		if _, err := stmt.Exec(h.ContainerID, scryfallID, h.Finish, h.Condition,
			h.Board, h.Quantity); err != nil {
			return fmt.Errorf("restoring %s (%s): %w", scryfallID, h.Finish, err)
		}
	}
	return tx.Commit()
}
