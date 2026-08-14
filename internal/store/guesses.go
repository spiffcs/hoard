package store

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

type FinishGuessRow struct {
	ID          int64
	ContainerID int64
	ScryfallID  string
	Name        string
	Set         string
	Number      string
	Finish      finish.Finish
	GuessedAt   string
}

func (s *Store) guessContainer(containerID int64) (int64, error) {
	if containerID != 0 {
		return containerID, nil
	}
	return s.collectionID()
}

func (s *Store) RecordFinishGuess(containerID int64, scryfallID string, fin finish.Finish) error {
	containerID, err := s.guessContainer(containerID)
	if err != nil {
		return fmt.Errorf("recording finish guess: %w", err)
	}
	_, err = s.db.Exec(`
INSERT INTO finish_guesses (container_id, scryfall_id, finish, guessed_at)
VALUES (?, ?, ?, ?)`, containerID, scryfallID, fin, now())
	if err != nil {
		return fmt.Errorf("recording finish guess: %w", err)
	}
	return nil
}

func (s *Store) ClearFinishGuess(containerID int64, scryfallID string, fin finish.Finish) error {
	containerID, err := s.guessContainer(containerID)
	if err != nil {
		return fmt.Errorf("clearing finish guess: %w", err)
	}
	_, err = s.db.Exec(`
DELETE FROM finish_guesses WHERE id = (
    SELECT id FROM finish_guesses
    WHERE container_id = ? AND scryfall_id = ? AND finish = ?
    ORDER BY id DESC LIMIT 1)`, containerID, scryfallID, fin)
	if err != nil {
		return fmt.Errorf("clearing finish guess: %w", err)
	}
	return nil
}

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
