package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

func (s *Store) SetHoldingQuantity(scryfallID string, fin finish.Finish, condition string, qty int) (previous int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	return s.SetHoldingQuantityIn(cid, scryfallID, fin, condition, qty)
}

func (s *Store) SetHoldingQuantityIn(containerID int64, scryfallID string, fin finish.Finish, condition string, qty int) (previous int, err error) {
	if err := validFinish(fin); err != nil {
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
		cid, scryfallID, fin, condition).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if qty <= 0 {
		if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
			cid, scryfallID, fin, condition); err != nil {
			return 0, err
		}
		return previous, tx.Commit()
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = excluded.quantity`,
		cid, scryfallID, fin, condition, qty); err != nil {
		return 0, err
	}
	return previous, tx.Commit()
}

func (s *Store) MoveEntry(fromContainer int64, scryfallID string, fin finish.Finish, condition string, toContainer int64, toScryfallID string) (prevTarget int, err error) {
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
		fromContainer, scryfallID, fin, condition).Scan(&qty)
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
		toContainer, toScryfallID, fin, condition).Scan(&prevTarget)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		toContainer, toScryfallID, fin, condition, qty); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		fromContainer, scryfallID, fin, condition); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

func (s *Store) MoveEntryFinish(containerID int64, scryfallID string, fromFinish, toFinish finish.Finish, condition string) (prevTarget int, err error) {
	if fromFinish == toFinish {
		return 0, nil
	}

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

func (s *Store) MoveCardFinish(scryfallID string, fromFinish, toFinish finish.Finish) (prevTarget int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}

	return s.MoveEntryFinish(cid, scryfallID, fromFinish, toFinish, ConditionUnknown)
}

func (s *Store) MoveEntryCondition(containerID int64, scryfallID string, fin finish.Finish, fromCondition, toCondition string) (prevTarget int, err error) {
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
		containerID, scryfallID, fin, fromCondition).Scan(&qty)
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
		containerID, scryfallID, fin, toCondition).Scan(&prevTarget)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		containerID, scryfallID, fin, toCondition, qty); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = 'main'`,
		containerID, scryfallID, fin, fromCondition); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

func (s *Store) RemoveFromCollection(scryfallID string) ([]Holding, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	return s.RemoveFromBinder(cid, scryfallID)
}

func (s *Store) RemoveFromBinder(containerID int64, scryfallID string) ([]Holding, error) {
	cid := containerID

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

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
