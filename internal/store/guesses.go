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

// RecordFinishGuess remembers that a scan committed this finish by default
// rather than by evidence. One row per commit: three guessed copies of one
// printing are three cards to go check, not one.
func (s *Store) RecordFinishGuess(containerID int64, scryfallID, finish string) error {
	_, err := s.db.Exec(`
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
	_, err := s.db.Exec(`
DELETE FROM finish_guesses WHERE id = (
    SELECT id FROM finish_guesses
    WHERE container_id = ? AND scryfall_id = ? AND finish = ?
    ORDER BY id DESC LIMIT 1)`, containerID, scryfallID, finish)
	if err != nil {
		return fmt.Errorf("clearing finish guess: %w", err)
	}
	return nil
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
