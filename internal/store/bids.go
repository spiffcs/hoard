package store

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
)

func (s *Store) BidSeries(scryfallID string, fin finish.Finish) ([]PricePoint, error) {
	rows, err := s.db.Query(`
SELECT as_of, price_usd, source
FROM card_bid_history
WHERE scryfall_id = ? AND finish = ?
ORDER BY as_of`, scryfallID, fin)
	if err != nil {
		return nil, fmt.Errorf("reading bid series for %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []PricePoint
	for rows.Next() {
		var p PricePoint
		if err := rows.Scan(&p.AsOf, &p.Price, &p.Source); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) BackfillBids(byCard map[string][]mtgjson.Observation) (inserted, cards int, err error) {
	return s.backfillHistory("card_bid_history", byCard)
}

type BidObservation struct {
	ScryfallID string
	Finish     finish.Finish
	Source     string
	Price      float64
}

func (s *Store) RecordBids(bids []BidObservation) (inserted int, err error) {
	if len(bids) == 0 {
		return 0, nil
	}
	last, err := s.latestBids()
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_bid_history (scryfall_id, finish, price_usd, source, as_of)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, as_of) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	ts := now()
	for _, b := range bids {
		fin := b.Finish
		if prev, ok := last[b.ScryfallID+"|"+fin.String()]; ok && prev == b.Price {
			continue
		}
		res, err := stmt.Exec(b.ScryfallID, fin, b.Price, b.Source, ts)
		if err != nil {
			return 0, fmt.Errorf("recording bid for %s: %w", b.ScryfallID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

func (s *Store) latestBids() (map[string]float64, error) {
	rows, err := s.db.Query(`
SELECT h.scryfall_id, h.finish, h.price_usd
FROM card_bid_history h
JOIN (SELECT scryfall_id, finish, MAX(as_of) AS newest
      FROM card_bid_history GROUP BY scryfall_id, finish) t
  ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.newest = h.as_of`)
	if err != nil {
		return nil, fmt.Errorf("reading latest bids: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var sid, finish string
		var price float64
		if err := rows.Scan(&sid, &finish, &price); err != nil {
			return nil, err
		}
		out[sid+"|"+finish] = price
	}
	return out, rows.Err()
}
