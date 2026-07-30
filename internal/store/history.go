package store

import (
	"cmp"
	"database/sql"
	"fmt"
	"slices"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

// PriceChange is one printing-and-finish whose price moved between two
// observations, alongside how much of it is held.
//
// Finish is the price's finish, 'normal' or 'foil'; etched copies are counted
// against the foil price, matching how entryValue values them.
type PriceChange struct {
	ScryfallID      string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Copies          int
	Old             float64
	New             float64
	// Source names where the new price came from: "scryfall", or the vendor
	// behind a fallback.
	Source string
}

// Delta is the movement in one copy's price.
func (p PriceChange) Delta() float64 { return p.New - p.Old }

// TotalDelta is what the movement is worth across every copy held. This is the
// figure worth sorting on: a $2 rise on a card owned forty times matters more
// than a $20 rise on a single copy.
func (p PriceChange) TotalDelta() float64 { return float64(p.Copies) * p.Delta() }

// Pct is the movement as a fraction of the old price, and is 0 when the card
// was previously worth nothing (any rise from zero is an infinite percentage,
// which is not a useful thing to sort or print).
func (p PriceChange) Pct() float64 {
	if p.Old == 0 {
		return 0
	}
	return p.Delta() / p.Old
}

// effectivePrices is every card's current price per finish, with the MTGJSON
// fallback applied, as two rows per card.
//
// It is the read side of the same COALESCE the valuation queries use, unpivoted
// so history can store one row per finish. Recording the raw Scryfall column
// instead would report a huge fake swing every time a card moved between a
// vendor fallback and a real Scryfall price.
const effectivePrices = `
    SELECT c.scryfall_id AS sid, 'normal' AS pfinish, ` + effPriceUSD + ` AS price,
           CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd, 'fallback') END AS source
    FROM cards c ` + altJoinCards + `
    UNION ALL
    SELECT c.scryfall_id, 'foil', ` + effPriceFoil + `,
           CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd_foil, 'fallback') END
    FROM cards c ` + altJoinCards

// ownedByPriceFinish is how many copies of each printing are held, in the two
// finishes prices come in. It spans the loose collection and every deck, because
// a card's worth does not depend on which box it sits in.
const ownedByPriceFinish = `
    SELECT scryfall_id AS sid,
           CASE WHEN finish IN ('foil','etched') THEN 'foil' ELSE 'normal' END AS pfinish,
           SUM(quantity) AS copies
    FROM card_entries
    GROUP BY sid, pfinish`

// latestPrices is the newest observation per card and finish. The %s takes an
// optional cutoff, which turns it into "the price as of then".
//
// Only the inner aggregate is filtered: as_of is unique per card and finish, so
// matching the outer row on it cannot pick up anything past the cutoff.
const latestPrices = `
    SELECT h.scryfall_id AS sid, h.finish AS pfinish, h.price_usd AS price, h.source AS source
    FROM card_price_history h
    JOIN (SELECT scryfall_id, finish, MAX(as_of) AS newest
          FROM card_price_history %s
          GROUP BY scryfall_id, finish) t
      ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.newest = h.as_of`

// RecordPrices appends an observation for every card whose effective price
// differs from the last one recorded, and reports what moved.
//
// Unchanged prices are not written: a row per card per refresh would grow the
// database by the size of the catalog daily, and nothing reads a repeat of a
// number already stored. A card with no history yet is recorded silently rather
// than reported, since a first observation is a baseline, not a movement.
//
// Only cards actually held are reported. History is kept for the whole catalog
// regardless, because a card can leave the collection and come back, and the
// gap in its series would be permanent.
//
// A price that disappears — Scryfall dropping a printing it used to quote —
// leaves the last known observation standing. These are observations, not
// assertions about every instant in between, and recording a null would make a
// card that merely went unquoted indistinguishable from one that crashed.
func (s *Store) RecordPrices() ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH eff AS (` + effectivePrices + `),
     owned AS (` + ownedByPriceFinish + `),
     latest AS (` + fmt.Sprintf(latestPrices, "") + `)
SELECT e.sid, e.pfinish, e.price, e.source, c.name, c.set_code, c.collector_number,
       COALESCE(o.copies, 0), l.price
FROM eff e
JOIN cards c ON c.scryfall_id = e.sid
LEFT JOIN latest l ON l.sid = e.sid AND l.pfinish = e.pfinish
LEFT JOIN owned o ON o.sid = e.sid AND o.pfinish = e.pfinish
WHERE e.price IS NOT NULL AND (l.price IS NULL OR l.price <> e.price)`)
	if err != nil {
		return nil, fmt.Errorf("reading price changes: %w", err)
	}
	defer rows.Close()

	var seen []PriceChange
	var moved []PriceChange
	for rows.Next() {
		var p PriceChange
		var old sql.NullFloat64
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.New, &p.Source, &p.Name,
			&p.SetCode, &p.CollectorNumber, &p.Copies, &old); err != nil {
			return nil, err
		}
		seen = append(seen, p)
		if old.Valid && p.Copies > 0 {
			p.Old = old.Float64
			moved = append(moved, p)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.appendPrices(seen); err != nil {
		return nil, err
	}
	return moved, nil
}

// appendPrices writes one observation per change, all at the same instant so a
// single refresh reads back as a single point in the series.
func (s *Store) appendPrices(changes []PriceChange) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Two refreshes within the same second collide on the primary key. The later
	// price is the truer one, so it wins rather than failing the whole run.
	stmt, err := tx.Prepare(`
INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, as_of) DO UPDATE SET
    price_usd = excluded.price_usd,
    source    = excluded.source`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, c := range changes {
		if _, err := stmt.Exec(c.ScryfallID, c.Finish, c.New, c.Source, ts); err != nil {
			return fmt.Errorf("recording price for %s: %w", c.Name, err)
		}
	}
	return tx.Commit()
}

// Movers reports every held printing whose price differs from what it was at
// since (an RFC3339 timestamp), newest price against the last one observed at or
// before that moment.
//
// A card first seen after the cutoff has no baseline and is left out: it has not
// moved, it has only just arrived, and treating its first price as a rise from
// nothing would put it top of every list.
func (s *Store) Movers(since string) ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH owned AS (`+ownedByPriceFinish+`),
     cur AS (`+fmt.Sprintf(latestPrices, "")+`),
     base AS (`+fmt.Sprintf(latestPrices, "WHERE as_of <= ?")+`)
SELECT c.scryfall_id, cur.pfinish, c.name, c.set_code, c.collector_number,
       o.copies, base.price, cur.price, cur.source
FROM owned o
JOIN cur ON cur.sid = o.sid AND cur.pfinish = o.pfinish
JOIN base ON base.sid = o.sid AND base.pfinish = o.pfinish
JOIN cards c ON c.scryfall_id = o.sid
WHERE cur.price <> base.price`, since)
	if err != nil {
		return nil, fmt.Errorf("reading movers: %w", err)
	}
	defer rows.Close()

	var out []PriceChange
	for rows.Next() {
		var p PriceChange
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.Name, &p.SetCode,
			&p.CollectorNumber, &p.Copies, &p.Old, &p.New, &p.Source); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PriceHistoryDepth reports how many observations are stored and when the oldest
// one was taken, so a command can say "no history yet" rather than "nothing
// moved" — the two look identical in an empty result and mean opposite things.
func (s *Store) PriceHistoryDepth() (observations int, oldest string, err error) {
	var first sql.NullString
	err = s.db.QueryRow(`SELECT COUNT(*), MIN(as_of) FROM card_price_history`).
		Scan(&observations, &first)
	if err != nil {
		return 0, "", err
	}
	return observations, first.String, nil
}

// backfillStamp turns MTGJSON's bare date into the RFC3339 form the rest of the
// history is written in.
//
// Midnight, not the hour the file was built: it puts a backfilled point before
// any live observation taken the same day, so the newest row for a card stays
// the one actually seen rather than the one reconstructed.
func backfillStamp(date string) string { return date + "T00:00:00Z" }

// BackfillPrices loads observations recorded before hoard was watching, keyed by
// Scryfall ID, and reports how many rows and how many cards it wrote.
//
// `before` bounds the import to the era with no history of its own — pass the
// oldest as_of from PriceHistoryDepth, or "" when there is none. The bound is
// not tidiness: MTGJSON's vendor snapshots are taken at a different hour than
// Scryfall's, so an imported point sitting alongside a real one for the same day
// would show up in Movers as a few cents of movement that never happened.
//
// Nothing already stored is overwritten. Where an imported row collides with a
// live one, the live one stands: it is what was observed, and this is what was
// reconstructed afterwards.
func (s *Store) BackfillPrices(byCard map[string][]mtgjson.Observation, before string) (inserted, cards int, err error) {
	if len(byCard) == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, as_of) DO NOTHING`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	for sid, obs := range byCard {
		var wrote bool
		for _, o := range compactSeries(obs, before) {
			res, err := stmt.Exec(sid, o.Finish, o.Price, o.Source, backfillStamp(o.Date))
			if err != nil {
				return 0, 0, fmt.Errorf("backfilling prices for %s: %w", sid, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted += int(n)
				wrote = true
			}
		}
		if wrote {
			cards++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return inserted, cards, nil
}

// compactSeries reduces one card's observations to the days its price actually
// moved, per finish, discarding anything at or after before.
//
// MTGJSON quotes a price for every one of its ninety days whether or not it
// changed, and storing all of them would add two orders of magnitude more rows
// than the collection has cards to say nothing new. The first point of each
// series is always kept: it is the baseline every later comparison reads back
// through.
//
// Filtering happens before compaction, not after, so the surviving window keeps
// a baseline of its own rather than inheriting one that got cut.
//
// The cutoff is compared by date rather than by timestamp. A live observation
// taken at 05:09 excludes that whole day, not just the hours after it: the two
// sources snapshot at different times, so an imported midnight price sitting
// under an observed morning one is the exact same-day overlap the bound exists
// to prevent.
func compactSeries(obs []mtgjson.Observation, before string) []mtgjson.Observation {
	if len(before) > len("2006-01-02") {
		before = before[:len("2006-01-02")]
	}
	byFinish := map[string][]mtgjson.Observation{}
	for _, o := range obs {
		if before != "" && o.Date >= before {
			continue
		}
		byFinish[o.Finish] = append(byFinish[o.Finish], o)
	}

	kept := make([]mtgjson.Observation, 0, len(byFinish))
	for _, series := range byFinish {
		slices.SortFunc(series, func(a, b mtgjson.Observation) int {
			return cmp.Compare(a.Date, b.Date)
		})
		var last float64
		for i, o := range series {
			if i == 0 || o.Price != last {
				kept = append(kept, o)
				last = o.Price
			}
		}
	}
	return kept
}
