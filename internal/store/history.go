package store

import (
	"cmp"
	"database/sql"
	"fmt"
	"math"
	"slices"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

// PriceChange is one printing-and-finish whose price moved between two
// observations, alongside how much of it is held.
//
// Finish is the price's finish, 'nonfoil' or 'foil'; etched copies are counted
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
	// ColorIdentity is the printing's WUBRG identity, nil when unknown —
	// same semantics as Card.ColorIdentity.
	ColorIdentity []string
	// Treatment is the foil treatment's display word, empty for plain —
	// same semantics as Card.Treatment.
	Treatment string
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

// MoverExtents scans the rows for each delta column's largest absolute
// value — CHANGE keyed on Pct, IMPACT on TotalDelta. The pairing lives
// here beside the type so the two frontends coloring those columns cannot
// disagree about which accessor feeds which scale.
func MoverExtents(rows []PriceChange) (pctMax, impactMax float64) {
	for _, c := range rows {
		pctMax = max(pctMax, math.Abs(c.Pct()))
		impactMax = max(impactMax, math.Abs(c.TotalDelta()))
	}
	return pctMax, impactMax
}

// effectivePrices is every card's current price per finish, with the MTGJSON
// fallback applied, as one row per finish per card.
//
// It is the read side of the same COALESCE the valuation queries use, unpivoted
// so history can store one row per finish. Recording the raw Scryfall column
// instead would report a huge fake swing every time a card moved between a
// vendor fallback and a real Scryfall price.
//
// Etched is emitted only where the card actually has an etched figure. Before
// v21 it shared the foil series, so an etched holding's history was the foil
// product's history; now it gets its own. Emitting it unconditionally would
// instead duplicate the foil series under a second name for the tens of
// thousands of printings that have no etched product at all.
const effectivePrices = `
    SELECT c.scryfall_id AS sid, 'nonfoil' AS pfinish, ` + effPriceUSD + ` AS price,
           CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd, 'fallback') END AS source
    FROM cards c ` + altJoinCards + `
    UNION ALL
    SELECT c.scryfall_id, 'foil', ` + effPriceFoil + `,
           CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd_foil, 'fallback') END
    FROM cards c ` + altJoinCards + `
    UNION ALL
    SELECT c.scryfall_id, 'etched', c.price_usd_etched, 'scryfall'
    FROM cards c WHERE c.price_usd_etched IS NOT NULL`

// ownedByPriceFinish is how many copies of each printing are held, in the
// finishes prices come in. It spans the loose collection and every deck, because
// a card's worth does not depend on which box it sits in.
//
// An etched holding counts as etched only where that printing has an etched
// price to count against; otherwise it falls in with foil, matching
// effPriceEtched's fallback. Without that check an etched copy of a printing
// priced only as a foil would join a series nothing ever writes, and its copies
// would vanish from movers entirely.
const ownedByPriceFinish = `
    SELECT e.scryfall_id AS sid,
           CASE WHEN e.finish = 'etched' AND c.price_usd_etched IS NOT NULL
                     THEN 'etched'
                WHEN e.finish IN ('foil','etched') THEN 'foil'
                ELSE 'nonfoil' END AS pfinish,
           SUM(e.quantity) AS copies
    FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
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
// Unchanged prices are not written, and a first observation is recorded silently:
// it is a baseline, not a movement. Only held cards are *reported*, but history is
// kept for everything, since a card can leave the collection and come back and the
// gap would be permanent.
//
// A price that disappears leaves the last observation standing. These are
// observations, not assertions about every instant between them.
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
	// One timestamp for the history rows and the snapshot, so the refresh
	// reads back as a single instant everywhere.
	ts := now()
	if err := s.appendPrices(seen, ts); err != nil {
		return nil, err
	}
	// The snapshot is written whether or not anything moved: a chart's flat
	// stretches are information ("checked, unchanged"), and skipping them
	// would leave gaps indistinguishable from "never looked".
	if err := s.snapshotValue(ts); err != nil {
		return nil, err
	}
	return moved, nil
}

// snapshotValue records what the whole hoard is worth right now, split the way
// the summary splits it. Uses entryValue, so the snapshot and the summary can
// never disagree about the same instant.
func (s *Store) snapshotValue(ts string) error {
	_, err := s.db.Exec(`
INSERT INTO value_snapshots (as_of, binder, decks, total, source)
SELECT ?,
       COALESCE(SUM(CASE WHEN ct.kind = '`+KindCollection+`' THEN e.quantity * `+entryValue+` END), 0),
       COALESCE(SUM(CASE WHEN ct.kind <> '`+KindCollection+`' THEN e.quantity * `+entryValue+` END), 0),
       COALESCE(SUM(e.quantity * `+entryValue+`), 0),
       'observed'
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
`+altJoinEntries+`
ON CONFLICT(as_of) DO UPDATE SET
    binder = excluded.binder,
    decks  = excluded.decks,
    total  = excluded.total,
    source = excluded.source`, ts)
	if err != nil {
		return fmt.Errorf("recording value snapshot: %w", err)
	}
	return nil
}

// ValuePoint is the hoard's worth at one instant. Seeded points were
// reconstructed at migration time from today's quantities and that day's
// prices — an estimate, and charts should say so.
type ValuePoint struct {
	AsOf   string
	Binder float64
	Decks  float64
	Total  float64
	Seeded bool
}

// ValueSnapshots returns the whole series, oldest first.
func (s *Store) ValueSnapshots() ([]ValuePoint, error) {
	rows, err := s.db.Query(`
SELECT as_of, binder, decks, total, source = 'seeded'
FROM value_snapshots ORDER BY as_of`)
	if err != nil {
		return nil, fmt.Errorf("reading value snapshots: %w", err)
	}
	defer rows.Close()
	var out []ValuePoint
	for rows.Next() {
		var p ValuePoint
		if err := rows.Scan(&p.AsOf, &p.Binder, &p.Decks, &p.Total, &p.Seeded); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// appendPrices writes one observation per change, all at the same instant so a
// single refresh reads back as a single point in the series.
func (s *Store) appendPrices(changes []PriceChange, ts string) error {
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
       o.copies, base.price, cur.price, cur.source, c.color_identity, c.promo_types
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
		var colors, promos sql.NullString
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.Name, &p.SetCode,
			&p.CollectorNumber, &p.Copies, &p.Old, &p.New, &p.Source, &colors, &promos); err != nil {
			return nil, err
		}
		p.ColorIdentity = parseColorIdentity(colors)
		p.Treatment = FoilTreatment(promos)
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

// BackfillPrices loads observations recorded before hoard was watching, and
// reports how many rows and cards it wrote.
//
// Each series is bounded to the era with no live history of its own: the
// import stops where that card and finish's own first observation begins.
// Not tidiness: the two sources snapshot at different hours, so an imported
// point beside a real one for the same day shows up in Movers as movement
// that never happened. Per card rather than hoard-wide, because a card
// added months into the hoard's history has months of archive the rest of
// the hoard already lived through — a single hoard-wide bound silently
// discarded all of it, and every deck imported after day one arrived with
// no baselines (observed live, the Tricky Terrain bug).
//
// Where an import collides with a live row the live one stands — it is what
// was observed, this is what was reconstructed. A previously *backfilled*
// series is the one exception: when the incoming import speaks for a
// different vendor, the old reconstruction retires first
// (retireSwitchedSeries), because two vendors' figures spliced into one
// series read as price movement that never happened.
func (s *Store) BackfillPrices(byCard map[string][]mtgjson.Observation) (inserted, cards int, err error) {
	return s.backfillHistory("card_price_history", byCard)
}

// backfillHistory is the shared import path for both history tables —
// retail prices and buylist bids follow the same doctrine, each bounded
// against its own table's live era. table is always one of the two
// package-internal table names, never caller input.
func (s *Store) backfillHistory(table string, byCard map[string][]mtgjson.Observation) (inserted, cards int, err error) {
	if len(byCard) == 0 {
		return 0, 0, nil
	}
	// MTGJSON speaks "normal"; the store has spoken "nonfoil" since
	// schema v8. Translate before anything reads the finishes so the
	// vendor-switch pass, the bound lookup, the series grouping and the
	// stored row all agree — untranslated rows join nothing and Movers
	// never sees them (schema v12 repaired the ones written before this
	// line existed).
	normalized := make(map[string][]mtgjson.Observation, len(byCard))
	for sid, obs := range byCard {
		ns := make([]mtgjson.Observation, len(obs))
		for i, o := range obs {
			o.Finish = storeFinish(o.Finish)
			ns[i] = o
		}
		normalized[sid] = ns
	}
	if err := s.retireSwitchedSeries(table, normalized); err != nil {
		return 0, 0, err
	}
	// After any retirements, so a replaced series' bound reflects only the
	// rows that actually survived.
	firstLive, err := s.firstObservationsIn(table)
	if err != nil {
		return 0, 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO ` + table + ` (scryfall_id, finish, price_usd, source, as_of)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, as_of) DO NOTHING`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	for sid, obs := range normalized {
		var wrote bool
		bound := func(finish string) string { return firstLive[sid+"|"+finish] }
		for _, o := range compactSeries(obs, bound) {
			res, err := stmt.Exec(sid, o.Finish, o.Price, o.Source, backfillStamp(o.Date))
			if err != nil {
				return 0, 0, fmt.Errorf("backfilling %s for %s: %w", table, sid, err)
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

// retireSwitchedSeries deletes a series' previously backfilled rows when
// the incoming import speaks for a different vendor — the vendor-switch
// case the per-series bound cannot handle alone. The old reconstruction
// would otherwise own the bound forever: the Manapool foil series written
// for the treated foils blocked TCGplayer's own series entirely once the
// tcgcsv overlay made it the chosen vendor (observed live: 152 of many
// thousand observations imported).
//
// Only reconstructed rows die — backfillStamp marks every import with a
// midnight timestamp, while live observations carry the clock time they
// were actually seen, so "live rows stand" survives the switch. A series
// with no incoming observations keeps its old reconstruction: deletion
// without a replacement would just erase history.
func (s *Store) retireSwitchedSeries(table string, byCard map[string][]mtgjson.Observation) error {
	incoming := map[string]string{} // sid|finish → the import's vendor
	for sid, obs := range byCard {
		for _, o := range obs {
			if o.Source != "" {
				incoming[sid+"|"+o.Finish] = o.Source
			}
		}
	}
	if len(incoming) == 0 {
		return nil
	}
	rows, err := s.db.Query(`
SELECT DISTINCT scryfall_id, finish, source FROM ` + table + `
WHERE as_of LIKE '%T00:00:00Z'`)
	if err != nil {
		return fmt.Errorf("reading backfilled sources: %w", err)
	}
	defer rows.Close()
	type series struct{ sid, finish string }
	var switched []series
	for rows.Next() {
		var sid, finish, source string
		if err := rows.Scan(&sid, &finish, &source); err != nil {
			return err
		}
		// Scryfall rows are live observations by definition — the import
		// never writes that source — so a midnight stamp on one (a
		// backdated fixture, a freak clock) still means "observed".
		if source == "scryfall" {
			continue
		}
		if want := incoming[sid+"|"+finish]; want != "" && want != source {
			switched = append(switched, series{sid, finish})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sw := range switched {
		if _, err := s.db.Exec(`
DELETE FROM `+table+`
WHERE scryfall_id = ? AND finish = ? AND as_of LIKE '%T00:00:00Z'
  AND source != ? AND source != 'scryfall'`,
			sw.sid, sw.finish, incoming[sw.sid+"|"+sw.finish]); err != nil {
			return fmt.Errorf("retiring %s series for %s: %w", table, sw.sid, err)
		}
	}
	return nil
}

// storeFinish translates MTGJSON's finish vocabulary into the schema's.
func storeFinish(finish string) string {
	if finish == "normal" {
		return "nonfoil"
	}
	return finish
}

// firstObservationsIn maps every (card, finish) with history in the given
// table to the moment its live history began — the per-series bound the
// backfill imports up to.
func (s *Store) firstObservationsIn(table string) (map[string]string, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, finish, MIN(as_of) FROM ` + table + `
GROUP BY scryfall_id, finish`)
	if err != nil {
		return nil, fmt.Errorf("reading history bounds: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sid, finish, oldest string
		if err := rows.Scan(&sid, &finish, &oldest); err != nil {
			return nil, err
		}
		out[sid+"|"+finish] = oldest
	}
	return out, rows.Err()
}

// compactSeries reduces one card's observations to the days its price moved, per
// finish, discarding anything at or after that finish's own bound.
//
// MTGJSON quotes every one of its ninety days whether or not the price changed;
// storing all of them would say nothing new in two orders of magnitude more rows.
// The first point of each series is always kept as the baseline.
//
// Filtering happens before compaction so the surviving window keeps a baseline of
// its own. The cutoff compares dates, not timestamps: the two sources snapshot at
// different hours, and an imported midnight price under an observed morning one is
// the same-day overlap the bound exists to prevent.
func compactSeries(obs []mtgjson.Observation, before func(finish string) string) []mtgjson.Observation {
	day := func(ts string) string {
		if len(ts) > len("2006-01-02") {
			return ts[:len("2006-01-02")]
		}
		return ts
	}
	byFinish := map[string][]mtgjson.Observation{}
	for _, o := range obs {
		if b := day(before(o.Finish)); b != "" && o.Date >= b {
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
