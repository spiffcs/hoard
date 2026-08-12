package store

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

// PriceChange is one printing-and-finish whose price moved between two
// observations, alongside how much of it is held.
//
// Finish is the price's finish — 'nonfoil', 'foil', or 'etched' where the
// printing has an etched figure of its own; an etched copy of a printing priced
// only as a foil is counted against the foil price, matching how entryValue
// values it.
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
	// Lang is the printing's language code ("en", "ja"), empty when the card's
	// document has not been stored — same semantics as Card.Lang.
	Lang string
	// OldAsOf is when Old was observed (RFC 3339), which is not always the
	// window's cutoff: a printing whose record begins inside the window is
	// measured from its own first price, so this row spans less time than the
	// window asked for. Empty where the comparison has no date behind it —
	// RecordPrices compares against the previous refresh, whatever its date.
	OldAsOf string
}

// Delta is the movement in one copy's price.
func (p PriceChange) Delta() float64 { return p.New - p.Old }

// TotalDelta is what the movement is worth across every copy held. This is the
// figure worth sorting on: a $2 rise on a card owned forty times matters more
// than a $20 rise on a single copy.
func (p PriceChange) TotalDelta() float64 { return float64(p.Copies) * p.Delta() }

// PctDefined reports whether Pct is a real figure. It is not when the card was
// previously worth nothing: any rise from zero is an infinite percentage, which
// is neither sortable nor printable.
//
// This is a predicate rather than an `if p.Old == 0` at each site because the
// two frontends want different things from the same answer — the table prints
// 0 so the column stays aligned, the document omits the field so no consumer
// reads a rise from nothing as no movement — and the one thing they must not do
// is disagree about which rows have a percentage at all.
func (p PriceChange) PctDefined() bool { return p.Old != 0 }

// Pct is the movement as a fraction of the old price, and is 0 where
// PctDefined is false.
func (p PriceChange) Pct() float64 {
	if !p.PctDefined() {
		return 0
	}
	return p.Delta() / p.Old
}

// BaselineFrom is the date to print beside Old on a row that measures less
// time than the window asked for: the printing's record begins inside the
// window, so Old is the series' own first price rather than the price at the
// cutoff, and the row spans from here rather than from the window's start.
//
// It is the empty string wherever the window's own date already describes the
// row — a baseline at or before the cutoff, or a comparison with no date
// behind it at all. Blank cells are what lets the column disappear entirely on
// a window the history covers, which is the common case and wants no column.
//
// The formatting lives here beside the type for the reason MoverExtents does:
// two frontends printing different dates for one row is the disagreement this
// forecloses.
func (p PriceChange) BaselineFrom(cutoff time.Time) string {
	if p.OldAsOf == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, p.OldAsOf)
	if err != nil || !t.After(cutoff) {
		return ""
	}
	return t.Local().Format("2 Jan")
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

// printedInFinish restricts a query to printings Scryfall says come in the
// named finish, assuming `c` is the cards table.
//
// A price series for a finish a printing does not exist in is not a price
// series. Manapool files one product's price under `normal` for a printing sold
// only as a foil, and the unpivot below took that at face value: the surge foil
// Aragorn and Arwen carried a "non-foil" history at $125.04 beside its real
// foil series, drawn as a second sparkline for a card that has no non-foil
// copy. Five such series existed in a live hoard.
//
// An unknown finishes list emits the row rather than suppressing it. Only a
// positive "this printing does not come that way" may drop a series; a printing
// whose Scryfall document has not been stored is a gap, and treating a gap as a
// denial would silently stop recording history for it.
func printedInFinish(finish string) string {
	return `(json_extract(c.raw_json, '$.finishes') IS NULL
         OR json_extract(c.raw_json, '$.finishes') LIKE '%"` + finish + `"%')`
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
//
// A var rather than a const only because printedInFinish is a function; the
// fragment is still assembled once and read everywhere, which is the property
// that matters.
// A correction names itself first, in the same order the price resolves. It
// has to: the source recorded here is what a percent watch anchors its series
// on (effSourceExpr), so a corrected price filed under 'scryfall' would put an
// observation in a series the anchor query then cannot find, and the figure
// would be permanently mislabelled in the one table that keeps a record.
var effectivePrices = `
    SELECT c.scryfall_id AS sid, 'nonfoil' AS pfinish, ` + effPriceUSD + ` AS price,
           CASE WHEN o.price_usd IS NOT NULL THEN o.source
                WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd, 'fallback') END AS source
    FROM cards c ` + altJoinCards + ` WHERE ` + printedInFinish("nonfoil") + `
    UNION ALL
    SELECT c.scryfall_id, 'foil', ` + effPriceFoil + `,
           CASE WHEN o.price_usd_foil IS NOT NULL THEN o.source
                WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                ELSE COALESCE(a.source_usd_foil, 'fallback') END
    FROM cards c ` + altJoinCards + ` WHERE ` + printedInFinish("foil") + `
    UNION ALL
    SELECT c.scryfall_id, 'etched', COALESCE(o.price_usd_etched, c.price_usd_etched),
           CASE WHEN o.price_usd_etched IS NOT NULL THEN o.source ELSE 'scryfall' END
    FROM cards c ` + altJoinCards + ` WHERE c.price_usd_etched IS NOT NULL`

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

// pricesAt is one observation per card and finish — whichever one the %s
// expression picks out of that series. The expression names an instant; the
// row carrying it comes back whole, price, source and date together.
//
// The choice is made in the inner aggregate rather than by filtering the outer
// join, because as_of is unique per card and finish: matching the outer row on
// the chosen instant can only ever return that one observation.
const pricesAt = `
    SELECT h.scryfall_id AS sid, h.finish AS pfinish, h.price_usd AS price,
           h.source AS source, h.as_of AS as_of
    FROM card_price_history h
    JOIN (SELECT scryfall_id, finish, %s AS chosen
          FROM card_price_history
          GROUP BY scryfall_id, finish) t
      ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.chosen = h.as_of`

// newestObservation picks the current price: the last one seen, whenever that
// was.
const newestObservation = `MAX(as_of)`

// windowBaseline picks where a price sat when the window opened, as well as the
// record can say: the last observation at or before the cutoff, or the earliest
// one the series has when the series itself begins inside the window.
//
// One rule, not a rule with an exception. A window is a range to measure
// movement across, not a precondition a card has to meet: asking for thirty
// days asks what moved in the last thirty days, not which cards carried a price
// thirty days ago. A printing first seen four days ago has four days of
// movement inside a thirty-day window, and those four days are the answer.
const windowBaseline = `COALESCE(MAX(CASE WHEN as_of <= ? THEN as_of END), MIN(as_of))`

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
     latest AS (` + fmt.Sprintf(pricesAt, newestObservation) + `)
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
	// One timestamp for the history rows and the snapshot, and one
	// transaction for both: written separately, an interrupt between them
	// left history rows with no snapshot at that instant — and the next
	// refresh, seeing the prices already recorded, wrote nothing, so the
	// value chart carried a permanent gap indistinguishable from "never
	// looked".
	ts := now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := appendPrices(tx, seen, ts); err != nil {
		return nil, err
	}
	// The snapshot is written whether or not anything moved: a chart's flat
	// stretches are information ("checked, unchanged"), and skipping them
	// would leave gaps indistinguishable from "never looked".
	if err := snapshotValue(tx, ts); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return moved, nil
}

// snapshotValue records what the whole hoard is worth right now, split the way
// the summary splits it. Uses entryValue, so the snapshot and the summary can
// never disagree about the same instant.
func snapshotValue(tx *sql.Tx, ts string) error {
	_, err := tx.Exec(`
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
// single refresh reads back as a single point in the series. It runs inside
// the caller's transaction, beside the value snapshot the observations imply.
func appendPrices(tx *sql.Tx, changes []PriceChange, ts string) error {
	if len(changes) == 0 {
		return nil
	}
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
	return nil
}

// Movers reports every held printing whose price moved inside the window that
// opens at since (an RFC3339 timestamp): its newest price against where its
// price sat when the window opened.
//
// since is a range to measure across, not a bar a printing has to clear. Where
// the record reaches back past it the baseline is the last observation at or
// before it; where the record begins inside the window the baseline is the
// series' own first price, and the row reports the movement it has. OldAsOf
// carries which one it was, per row, so a reader is never told a figure came
// from a date it did not come from.
//
// Left out is the one case with nothing to compare: a printing with a single
// observation. That is what base.as_of < cur.as_of says — a lone price is its
// own baseline, and a first price read as a rise from nothing would put every
// newly priced card top of every list. It is stated as a date comparison rather
// than left to fall out of the price test, because it is a rule about having
// two prices, not about their happening to differ.
func (s *Store) Movers(since string) ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH owned AS (`+ownedByPriceFinish+`),
     cur AS (`+fmt.Sprintf(pricesAt, newestObservation)+`),
     base AS (`+fmt.Sprintf(pricesAt, windowBaseline)+`)
SELECT c.scryfall_id, cur.pfinish, c.name, c.set_code, c.collector_number,
       o.copies, base.price, cur.price, cur.source, c.color_identity, c.promo_types,
       COALESCE(c.lang, ''), base.as_of
FROM owned o
JOIN cur ON cur.sid = o.sid AND cur.pfinish = o.pfinish
JOIN base ON base.sid = o.sid AND base.pfinish = o.pfinish
JOIN cards c ON c.scryfall_id = o.sid
WHERE base.as_of < cur.as_of AND cur.price <> base.price`, since)
	if err != nil {
		return nil, fmt.Errorf("reading movers: %w", err)
	}
	defer rows.Close()

	var out []PriceChange
	for rows.Next() {
		var p PriceChange
		var colors, promos sql.NullString
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.Name, &p.SetCode,
			&p.CollectorNumber, &p.Copies, &p.Old, &p.New, &p.Source, &colors, &promos,
			&p.Lang, &p.OldAsOf); err != nil {
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
	// One transaction around the whole import — the retirements and the
	// replacement inserts live or die together. The retire pass used to
	// commit its DELETEs on the spot, before this transaction began, so an
	// interrupt in the gap destroyed a card's reconstructed series with
	// nothing put back — and MTGJSON reaches back only 90 days, so anything
	// older was gone for good. The package's own doctrine (main.go: "every
	// write is a single transaction") now actually holds here.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if err := retireSwitchedSeries(tx, table, normalized); err != nil {
		return 0, 0, err
	}
	// After any retirements, so a replaced series' bound reflects only the
	// rows that actually survived.
	firstLive, err := firstObservationsIn(tx, table)
	if err != nil {
		return 0, 0, err
	}

	stmt, err := tx.Prepare(`
INSERT INTO ` + table + ` (scryfall_id, finish, price_usd, source, as_of)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, as_of) DO NOTHING`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	// The same rule the live unpivot applies (printedInFinish): an import
	// carries whatever buckets the vendor filed, and a vendor that files one
	// product under `normal` for a foil-only printing would otherwise
	// reconstruct ninety days of a series that describes no card anyone owns.
	printed, err := printedFinishes(tx)
	if err != nil {
		return 0, 0, err
	}

	for sid, obs := range normalized {
		var wrote bool
		bound := func(finish string) string { return firstLive[sid+"|"+finish] }
		for _, o := range compactSeries(obs, bound) {
			if f, known := printed[sid]; known && !f[o.Finish] {
				continue
			}
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
// It runs inside the backfill's own transaction: a retirement whose
// replacement never lands must roll back with it.
func retireSwitchedSeries(tx *sql.Tx, table string, byCard map[string][]mtgjson.Observation) error {
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
	rows, err := tx.Query(`
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
		if _, err := tx.Exec(`
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
func firstObservationsIn(tx *sql.Tx, table string) (map[string]string, error) {
	rows, err := tx.Query(`
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

// printedFinishes maps each printing to the finishes Scryfall says it comes
// in. A printing absent from the map has no stored document, and the caller
// must treat that as "unknown" rather than "none" — see printedInFinish.
//
// Etched is deliberately folded in with foil rather than read literally.
// Scryfall lists "etched" as its own finish, but the history table stores an
// etched observation under 'etched' only where the printing carries an etched
// price of its own; everywhere else effPriceEtched values it as the foil, and
// dropping a foil-bucket import for an etched-only printing would leave that
// holding with no series at all.
func printedFinishes(tx *sql.Tx) (map[string]map[string]bool, error) {
	rows, err := tx.Query(`
SELECT scryfall_id, json_extract(raw_json, '$.finishes')
FROM cards WHERE json_extract(raw_json, '$.finishes') IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var sid, list string
		if err := rows.Scan(&sid, &list); err != nil {
			return nil, err
		}
		var finishes []string
		if err := json.Unmarshal([]byte(list), &finishes); err != nil {
			continue // an unreadable list is not a denial
		}
		set := make(map[string]bool, len(finishes)+1)
		for _, f := range finishes {
			set[storeFinish(f)] = true
		}
		if set["etched"] {
			set["foil"] = true
		}
		if len(set) > 0 {
			out[sid] = set
		}
	}
	return out, rows.Err()
}
