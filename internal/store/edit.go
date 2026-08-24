package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

const BoardMain = "main"

type EntryRef struct {
	ContainerID int64
	ScryfallID  string
	Finish      finish.Finish
	Condition   string
	Board       string
}

func (h Holding) Ref() EntryRef {
	return EntryRef{ContainerID: h.ContainerID, ScryfallID: h.ScryfallID,
		Finish: h.Finish, Condition: h.Condition, Board: h.Board}
}

func (r EntryRef) normalized() EntryRef {
	r.Condition = orUnknown(r.Condition)
	if r.Board == "" {
		r.Board = BoardMain
	}
	return r
}

func (r EntryRef) with(fin finish.Finish, condition string) EntryRef {
	r.Finish, r.Condition = fin, condition
	return r
}

const selectEntryQuantity = `
SELECT quantity FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = ?`

const deleteEntry = `
DELETE FROM card_entries
WHERE container_id = ? AND scryfall_id = ? AND finish = ? AND condition = ?
  AND board = ?`

const insertEntryReplacing = `
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = excluded.quantity`

const insertEntryAdding = `
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`

func (r EntryRef) args() []any {
	return []any{r.ContainerID, r.ScryfallID, r.Finish, r.Condition, r.Board}
}

func entryQuantity(tx *sql.Tx, r EntryRef) (int, error) {
	var qty int
	err := tx.QueryRow(selectEntryQuantity, r.args()...).Scan(&qty)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	return qty, nil
}

func (s *Store) SetHoldingQuantity(scryfallID string, fin finish.Finish, condition string, qty int) (previous int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	return s.SetEntryQuantity(EntryRef{ContainerID: cid, ScryfallID: scryfallID,
		Finish: fin, Condition: condition, Board: BoardMain}, qty)
}

func (s *Store) SetEntryQuantity(ref EntryRef, qty int) (previous int, err error) {
	if err := validFinish(ref.Finish); err != nil {
		return 0, err
	}
	ref = ref.normalized()
	if err := validCondition(ref.Condition); err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if previous, err = entryQuantity(tx, ref); err != nil {
		return 0, err
	}

	if qty <= 0 {
		if _, err := tx.Exec(deleteEntry, ref.args()...); err != nil {
			return 0, err
		}
		return previous, tx.Commit()
	}

	if _, err := tx.Exec(insertEntryReplacing, append(ref.args(), qty)...); err != nil {
		return 0, err
	}
	return previous, tx.Commit()
}

func moveEntryTx(tx *sql.Tx, from, to EntryRef) (prevTarget int, err error) {
	var qty int
	err = tx.QueryRow(selectEntryQuantity, from.args()...).Scan(&qty)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no such holding to move")
	}
	if err != nil {
		return 0, err
	}
	if prevTarget, err = entryQuantity(tx, to); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(insertEntryAdding, append(to.args(), qty)...); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(deleteEntry, from.args()...); err != nil {
		return 0, err
	}
	return prevTarget, nil
}

func (s *Store) MoveEntry(from EntryRef, toContainer int64, toScryfallID string) (prevTarget int, err error) {
	from = from.normalized()
	if from.ContainerID == toContainer && from.ScryfallID == toScryfallID {
		return 0, nil
	}
	to := from
	to.ContainerID, to.ScryfallID = toContainer, toScryfallID

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if prevTarget, err = moveEntryTx(tx, from, to); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

func (s *Store) MoveEntryFinish(from EntryRef, toFinish finish.Finish) (prevTarget int, err error) {
	from = from.normalized()
	if from.Finish == toFinish {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if prevTarget, err = moveEntryTx(tx, from, from.with(toFinish, from.Condition)); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
DELETE FROM finish_guesses WHERE id = (
    SELECT id FROM finish_guesses
    WHERE container_id = ? AND scryfall_id = ? AND finish = ?
    ORDER BY id DESC LIMIT 1)`,
		from.ContainerID, from.ScryfallID, from.Finish); err != nil {
		return 0, err
	}
	return prevTarget, tx.Commit()
}

func (s *Store) MoveCardFinish(scryfallID string, fromFinish, toFinish finish.Finish) (prevTarget int, err error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}

	return s.MoveEntryFinish(EntryRef{ContainerID: cid, ScryfallID: scryfallID,
		Finish: fromFinish, Condition: ConditionUnknown, Board: BoardMain}, toFinish)
}

func (s *Store) MoveEntryCondition(from EntryRef, toCondition string) (prevTarget int, err error) {
	from = from.normalized()
	toCondition = orUnknown(toCondition)
	if from.Condition == toCondition {
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

	if prevTarget, err = moveEntryTx(tx, from, from.with(from.Finish, toCondition)); err != nil {
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

	stmt, err := tx.Prepare(insertEntryReplacing)
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

func (s *Store) MoveEntries(refs []EntryRef, toContainer int64) (moved int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, ref := range refs {
		from := ref.normalized()
		if from.ContainerID == toContainer {
			continue
		}
		qty, err := entryQuantity(tx, from)
		if err != nil {
			return 0, err
		}
		to := from
		to.ContainerID = toContainer
		if _, err := moveEntryTx(tx, from, to); err != nil {
			return 0, err
		}
		moved += qty
	}
	return moved, tx.Commit()
}
