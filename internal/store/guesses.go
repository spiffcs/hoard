package store

import "fmt"

// FinishGuessRow is one scanner commit whose finish nothing on the card chose,
// joined to the card it named so the list is checkable against the physical
// pile without another query.
type FinishGuessRow struct {
	ID          int64
	ContainerID int64
	ScryfallID  string
	Name        string
	Set         string
	Number      string
	Finish      string
	GuessedAt   string
}

// guessContainer resolves the adder's "default binder" sentinel (0) to the
// real collection container before a guess row is keyed on it. Stored
// verbatim, the 0 matched no container, so default-binder guesses were
// invisible to every join and unclearable by the editors (which pass real
// ids). Migration v25 repoints rows written before this existed.
func (s *Store) guessContainer(containerID int64) (int64, error) {
	if containerID != 0 {
		return containerID, nil
	}
	return s.collectionID()
}

// RecordFinishGuess remembers that a scan committed this finish by default
// rather than by evidence. One row per commit: three guessed copies of one
// printing are three cards to go check, not one.
func (s *Store) RecordFinishGuess(containerID int64, scryfallID, finish string) error {
	containerID, err := s.guessContainer(containerID)
	if err != nil {
		return fmt.Errorf("recording finish guess: %w", err)
	}
	_, err = s.db.Exec(`
INSERT INTO finish_guesses (container_id, scryfall_id, finish, guessed_at)
VALUES (?, ?, ?, ?)`, containerID, scryfallID, finish, now())
	if err != nil {
		return fmt.Errorf("recording finish guess: %w", err)
	}
	return nil
}

// ClearFinishGuess forgets the newest matching guess — evidence arrived, from
// a better read or a human who checked the card. One row, not all of them: a
// correction answers for the commit it corrected and nothing more.
func (s *Store) ClearFinishGuess(containerID int64, scryfallID, finish string) error {
	containerID, err := s.guessContainer(containerID)
	if err != nil {
		return fmt.Errorf("clearing finish guess: %w", err)
	}
	_, err = s.db.Exec(`
DELETE FROM finish_guesses WHERE id = (
    SELECT id FROM finish_guesses
    WHERE container_id = ? AND scryfall_id = ? AND finish = ?
    ORDER BY id DESC LIMIT 1)`, containerID, scryfallID, finish)
	if err != nil {
		return fmt.Errorf("clearing finish guess: %w", err)
	}
	return nil
}

// ConfirmFinishGuess retires one guess by its row id: a human looked at the
// physical card and the scanner had it right after all.
//
// This is the half ClearFinishGuess was missing. A correction was the only
// evidence the log could take, so a guess that happened to be correct produced
// no re-key, kept no way to be answered, and stayed listed for good. The queue
// could only grow, which made its count a running total of everything ever
// scanned blind rather than a count of cards still to check. A confirmation is
// evidence too, and it retires the row the same way a correction does.
//
// By row id rather than by container, card and finish, because the log is per
// commit: two copies of one printing bank two rows that no key can tell apart.
// A correction may take the newest of them, since it genuinely answers for
// whichever copy it re-keyed. A confirmation answers for the single card in
// the reader's hand, so it has to name that row and no other.
//
// Reports whether a row was there, so a caller can tell a guess it retired
// from an id that named nothing.
func (s *Store) ConfirmFinishGuess(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM finish_guesses WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("confirming finish guess %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("confirming finish guess %d: %w", id, err)
	}
	return n > 0, nil
}

// GuessedFinishes lists every commit still standing on a guessed finish,
// newest first — the audit queue for a pile scanned under a bad lamp.
func (s *Store) GuessedFinishes() ([]FinishGuessRow, error) {
	rows, err := s.db.Query(`
SELECT g.id, g.container_id, g.scryfall_id, c.name, c.set_code, c.collector_number,
       g.finish, g.guessed_at
FROM finish_guesses g
JOIN cards c ON c.scryfall_id = g.scryfall_id
ORDER BY g.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinishGuessRow
	for rows.Next() {
		var r FinishGuessRow
		if err := rows.Scan(&r.ID, &r.ContainerID, &r.ScryfallID, &r.Name,
			&r.Set, &r.Number, &r.Finish, &r.GuessedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
