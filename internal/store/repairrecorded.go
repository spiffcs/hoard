package store

import (
	"database/sql"
	"fmt"
)

func (s *Store) RepairRecordedPrices() (moved []PriceChange, repaired int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	var at sql.NullString
	if err := tx.QueryRow(`SELECT MAX(as_of) FROM value_snapshots`).Scan(&at); err != nil {
		return nil, 0, fmt.Errorf("finding the last recording: %w", err)
	}
	if !at.Valid || at.String == "" {
		return nil, 0, nil
	}
	ts := at.String

	before, err := observationsAt(tx, ts)
	if err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM card_price_history WHERE as_of = ?`, ts); err != nil {
		return nil, 0, fmt.Errorf("clearing the recording at %s: %w", ts, err)
	}

	if _, err := tx.Exec(`
INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT e.sid, e.pfinish, e.price, e.source, ?1
FROM (`+effectivePrices+`) e
LEFT JOIN (`+fmt.Sprintf(observationsBefore, "?1")+`) prev
       ON prev.sid = e.sid AND prev.pfinish = e.pfinish
WHERE e.price IS NOT NULL AND (prev.price IS NULL OR prev.price <> e.price)`, ts); err != nil {
		return nil, 0, fmt.Errorf("re-recording at %s: %w", ts, err)
	}

	after, err := observationsAt(tx, ts)
	if err != nil {
		return nil, 0, err
	}
	repaired = countDifferences(before, after)

	if err := snapshotValue(tx, ts); err != nil {
		return nil, 0, err
	}
	if err := s.clearTrendStats(tx); err != nil {
		return nil, 0, err
	}
	if moved, err = changesAt(tx, ts); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return moved, repaired, nil
}

const observationsBefore = `
    SELECT h.scryfall_id AS sid, h.finish AS pfinish, h.price_usd AS price
    FROM card_price_history h
    JOIN (SELECT scryfall_id, finish, MAX(as_of) AS chosen
          FROM card_price_history WHERE as_of < %[1]s
          GROUP BY scryfall_id, finish) t
      ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.chosen = h.as_of`

type recordedPrice struct {
	price  float64
	source string
}

func observationsAt(tx *sql.Tx, ts string) (map[string]recordedPrice, error) {
	rows, err := tx.Query(`
SELECT scryfall_id, finish, price_usd, source FROM card_price_history WHERE as_of = ?`, ts)
	if err != nil {
		return nil, fmt.Errorf("reading the recording at %s: %w", ts, err)
	}
	defer rows.Close()

	out := map[string]recordedPrice{}
	for rows.Next() {
		var sid, finish, source string
		var price float64
		if err := rows.Scan(&sid, &finish, &price, &source); err != nil {
			return nil, err
		}
		out[sid+"|"+finish] = recordedPrice{price, source}
	}
	return out, rows.Err()
}

func countDifferences(before, after map[string]recordedPrice) int {
	n := 0
	for key, b := range before {
		if a, ok := after[key]; !ok || a != b {
			n++
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			n++
		}
	}
	return n
}

func changesAt(tx *sql.Tx, ts string) ([]PriceChange, error) {
	rows, err := tx.Query(`
WITH owned AS (`+ownedByPriceFinish+`),
     prev AS (`+fmt.Sprintf(observationsBefore, "?1")+`)
SELECT n.scryfall_id, n.finish, n.price_usd, n.source, c.name, c.set_code,
       c.collector_number, COALESCE(c.released_at, ''), o.copies, prev.price
FROM card_price_history n
JOIN cards c ON c.scryfall_id = n.scryfall_id
JOIN owned o ON o.sid = n.scryfall_id AND o.pfinish = n.finish
JOIN prev ON prev.sid = n.scryfall_id AND prev.pfinish = n.finish
WHERE n.as_of = ?1 AND o.copies > 0`, ts)
	if err != nil {
		return nil, fmt.Errorf("reading what moved at %s: %w", ts, err)
	}
	defer rows.Close()

	var out []PriceChange
	for rows.Next() {
		var p PriceChange
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.New, &p.Source, &p.Name,
			&p.SetCode, &p.CollectorNumber, &p.ReleasedAt, &p.Copies, &p.Old); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
