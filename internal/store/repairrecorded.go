package store

// Repairing the observation a recording wrote, after the corrections that
// should have informed it arrive late.
//
// The corrections are the contradicted-price sweep: one paced vendor request
// per owned TCGplayer group, twenty seconds on a large hoard, and the slowest
// thing a price update does. Recording used to wait behind all of it, because
// an observation taken before the corrections logs the figure hoard refuses
// rather than the one it reports — a refused $0.56 entering the series shows up
// in movers as a crash and a recovery that never happened.
//
// Waiting was the wrong trade. Corrections are rare (one printing in 2,104 on
// the hoard this was measured against), and gating every card's history on a
// pass that almost never changes anything put twenty seconds in front of the
// numbers a reader was waiting for. So the recording goes first and this puts
// it right afterwards, for the handful of series that turn out to need it.
//
// "Put it right" is not "delete the bad row". The sweep can move a series in
// three directions, and only one of them is a wrong row that needs replacing:
//
//   - a price newly refused: the recorded figure is the degenerate one and must
//     become the ask that replaced it
//   - a correction retired because the feed came back to its senses: the
//     recorded figure is yesterday's ask and must become the feed's own price
//   - a correction that lands exactly back on the previously recorded price:
//     nothing moved after all, and the row must go, or the series carries a
//     round trip that never happened
//
// The third is why this re-decides the whole recording rather than patching
// rows. It deletes what the recording wrote and applies the same rule again
// against corrected prices — a row exists only where the price differs from the
// observation before it — so whatever the sweep did, the result is the history
// the recording would have written had it run second.

import (
	"database/sql"
	"fmt"
)

// RepairRecordedPrices re-decides the last recording against the corrections
// now in force, and reports what actually moved.
//
// It targets the instant that recording used, found in value_snapshots:
// RecordPrices writes one there unconditionally, in the same transaction and at
// the same timestamp as the history rows, so the newest snapshot is a faithful
// record of when the last recording happened. That coupling is load-bearing in
// both directions — this reads it to find the rows, and rewrites the snapshot
// once the prices under it have changed, since a total computed from a refused
// figure is as wrong as the observation was.
//
// A store that has never recorded has nothing to repair and says so quietly.
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
	// The recording rule, re-applied: an observation where the effective price
	// differs from the one before it, and none where it does not. The
	// comparison is against as_of < ts rather than "the newest", because the
	// rows this just deleted were the newest.
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

	// The snapshot rides the same instant and the same prices, so it is rebuilt
	// here too — its UPSERT on as_of replaces the total in place rather than
	// leaving a second point a moment later.
	if err := snapshotValue(tx, ts); err != nil {
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

// observationsBefore is the newest observation strictly before an instant, per
// card and finish. The instant is a placeholder reference, not a value — it is
// substituted twice in one statement, which is why it is formatted in.
const observationsBefore = `
    SELECT h.scryfall_id AS sid, h.finish AS pfinish, h.price_usd AS price
    FROM card_price_history h
    JOIN (SELECT scryfall_id, finish, MAX(as_of) AS chosen
          FROM card_price_history WHERE as_of < %[1]s
          GROUP BY scryfall_id, finish) t
      ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.chosen = h.as_of`

// recordedPrice is one observation as this file compares them.
type recordedPrice struct {
	price  float64
	source string
}

// observationsAt reads the rows written at one instant, keyed by series.
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

// countDifferences counts the series whose recorded observation changed —
// rewritten, newly written, or withdrawn.
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

// changesAt reports what the observation at one instant says moved, under the
// same rule RecordPrices reports by: a held printing with something to compare
// against. A first observation is a baseline, not a movement.
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
