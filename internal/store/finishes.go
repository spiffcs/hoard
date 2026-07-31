package store

import (
	"fmt"
	"slices"
)

// Reconciling a stored finish with the ones a printing actually comes in.

// FinishFix is one entry recorded in a finish its printing does not come in.
type FinishFix struct {
	Name            string
	SetCode         string
	CollectorNumber string
	Container       string
	Board           string
	From            string
	// To is the finish to store instead. Set only for corrected rows; an
	// ambiguous row carries its candidates in Available instead.
	To string
	// Available is the printing's real finishes, for rows where no single
	// correction is safe and the caller can only report the choices.
	Available []string
	Quantity  int
}

// FinishIsAvailable reports whether a printing comes in the given finish.
// available is Scryfall's finishes list for that printing; an empty list means
// unknown, which counts as available since there is nothing to contradict it.
// Since schema v8 the store speaks Scryfall's own finish vocabulary
// (nonfoil|foil|etched), so this is a plain membership test.
func FinishIsAvailable(finish string, available []string) bool {
	if len(available) == 0 {
		return true
	}
	return slices.Contains(available, finish)
}

// CorrectFinish returns the finish an entry should use and whether that differs
// from what was asked for.
//
// It only corrects when the printing comes in exactly one finish, since that is
// the only case with a single right answer. A printing available in several
// finishes where the recorded one is not among them is left alone: guessing
// there would be worse than reporting it.
//
// Shared by deck import and repair-finishes so both apply the same rule.
func CorrectFinish(finish string, available []string) (string, bool) {
	if FinishIsAvailable(finish, available) || len(available) != 1 {
		return finish, false
	}
	return available[0], true
}

// RepairFinishes corrects entries whose finish does not exist for that printing.
//
// A decklist with no foil marker imports as "nonfoil", but plenty of printings
// are foil-only: precon commanders and Duel Decks reprints among them. The
// resulting entry asks for a price that cannot exist, so the card is valued at
// zero forever and no amount of price fetching helps.
//
// available maps a Scryfall ID to the finishes that printing actually comes in.
// A correction is only made when the printing has exactly one finish, since that
// is the only case with a single right answer; anything else is returned as
// ambiguous and left untouched rather than guessed at.
func (s *Store) RepairFinishes(available map[string][]string) (fixed, ambiguous []FinishFix, err error) {
	// The read happens inside the same transaction as the writes below.
	// Quantities captured by the SELECT are re-written by the merge, so a
	// change between the two — another hoard process, the browse TUI in a
	// second terminal — would silently lose or invent copies.
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
SELECT e.container_id, ` + containerLabel + `, e.scryfall_id, e.finish, e.board, e.quantity,
       c.name, c.set_code, c.collector_number
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
ORDER BY c.name`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading entries: %w", err)
	}
	defer rows.Close()

	type target struct {
		containerID int64
		scryfallID  string
		board       string
		from, to    string
		quantity    int
	}
	var todo []target
	for rows.Next() {
		var t target
		var f FinishFix
		if err := rows.Scan(&t.containerID, &f.Container, &t.scryfallID, &t.from,
			&t.board, &t.quantity, &f.Name, &f.SetCode, &f.CollectorNumber); err != nil {
			return nil, nil, err
		}
		finishes, known := available[t.scryfallID]
		if !known || len(finishes) == 0 {
			continue // nothing to judge against
		}
		if FinishIsAvailable(t.from, finishes) {
			continue // the recorded finish is real
		}

		f.Board, f.From, f.Quantity = t.board, t.from, t.quantity
		to, ok := CorrectFinish(t.from, finishes)
		if !ok {
			f.Available = finishes
			ambiguous = append(ambiguous, f)
			continue
		}
		t.to, f.To = to, to
		fixed = append(fixed, f)
		todo = append(todo, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// The single connection serializes statements, so the cursor must be fully
	// drained and closed before the writes below reuse the connection.
	rows.Close()
	if len(todo) == 0 {
		return nil, ambiguous, nil
	}

	for _, t := range todo {
		// Insert-then-delete rather than UPDATE: the corrected finish may
		// already exist for this card in this container, and the primary key
		// includes finish. Merging the quantities is the only correct outcome.
		if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
			t.containerID, t.scryfallID, t.to, t.board, t.quantity); err != nil {
			return nil, nil, fmt.Errorf("moving entry to %s: %w", t.to, err)
		}
		if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id=? AND scryfall_id=? AND finish=? AND board=?`,
			t.containerID, t.scryfallID, t.from, t.board); err != nil {
			return nil, nil, fmt.Errorf("removing old %s entry: %w", t.from, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return fixed, ambiguous, nil
}
