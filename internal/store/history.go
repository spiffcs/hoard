package store

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
)

type PriceChange struct {
	ScryfallID      string
	Name            string
	SetCode         string
	CollectorNumber string

	ReleasedAt string
	Finish     finish.Finish
	Copies     int
	Old        float64
	New        float64

	Source string

	ColorIdentity []string

	Treatment string

	Lang string

	OldAsOf string
}

func (p PriceChange) Delta() float64 { return p.New - p.Old }

func (p PriceChange) TotalDelta() float64 { return float64(p.Copies) * p.Delta() }

const DefaultSettlingDays = 90

const SettlingDaysEnv = "HOARD_SETTLING_DAYS"

var settlingDays = func() *atomic.Int64 {
	var v atomic.Int64
	v.Store(-1)
	return &v
}()

func settlingDaysFrom(raw string) int {
	if n, ok := settlingDaysAsked(raw); ok {
		return n
	}
	return DefaultSettlingDays
}

func settlingDaysAsked(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func SettlingDaysEnvAsk() (int, bool) { return settlingDaysAsked(os.Getenv(SettlingDaysEnv)) }

func SettlingDays() int {
	if v := settlingDays.Load(); v >= 0 {
		return int(v)
	}
	settlingDays.CompareAndSwap(-1, int64(settlingDaysFrom(os.Getenv(SettlingDaysEnv))))
	return int(settlingDays.Load())
}

func SetSettlingDays(days int) {
	if days < 0 {
		days = DefaultSettlingDays
	}
	settlingDays.Store(int64(days))
}

func (p PriceChange) Settling(now time.Time) bool { return Settling(p.ReleasedAt, now) }

func Settling(releasedAt string, now time.Time) bool {
	return SettlingAt(releasedAt, SettlingCutoff(now))
}

func SettlingCutoff(now time.Time) string {
	days := SettlingDays()
	if days <= 0 {
		return ""
	}
	return now.AddDate(0, 0, -days).Format(time.DateOnly)
}

func SettlingAt(releasedAt, cutoff string) bool {
	return cutoff != "" && releasedAt != "" && releasedAt > cutoff
}

func NetMoved(rows []PriceChange, now time.Time) (net float64, heldOutSets int) {
	var held map[string]struct{}
	for _, c := range rows {
		if c.Settling(now) {
			if held == nil {
				held = make(map[string]struct{})
			}
			held[c.SetCode] = struct{}{}
			continue
		}
		net += c.TotalDelta()
	}
	return net, len(held)
}

func (p PriceChange) PctDefined() bool { return p.Old != 0 }

func (p PriceChange) Pct() float64 {
	if !p.PctDefined() {
		return 0
	}
	return p.Delta() / p.Old
}

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

func MoverExtents(rows []PriceChange) (pctMax, impactMax float64) {
	for _, c := range rows {
		pctMax = max(pctMax, math.Abs(c.Pct()))
		impactMax = max(impactMax, math.Abs(c.TotalDelta()))
	}
	return pctMax, impactMax
}

func printedInFinish(finish string) string {
	return `(json_extract(c.raw_json, '$.finishes') IS NULL
         OR json_extract(c.raw_json, '$.finishes') LIKE '%"` + finish + `"%')`
}

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

const ownedByPriceFinish = `
    SELECT e.scryfall_id AS sid,
           CASE WHEN e.finish = 'etched'
                     AND (SELECT price_usd_etched FROM cards
                          WHERE scryfall_id = e.scryfall_id) IS NOT NULL
                     THEN 'etched'
                WHEN e.finish IN ('foil','etched') THEN 'foil'
                WHEN e.finish = 'nonfoil' THEN 'nonfoil' END AS pfinish,
           SUM(` + countedQuantity + `) AS copies
    FROM card_entries e
    JOIN containers ctc ON ctc.id = e.container_id
    GROUP BY sid, pfinish`

const pricesAt = `
    SELECT h.scryfall_id AS sid, h.finish AS pfinish, h.price_usd AS price,
           h.source AS source, h.as_of AS as_of
    FROM card_price_history h
    JOIN (SELECT scryfall_id, finish, %s AS chosen
          FROM card_price_history
          GROUP BY scryfall_id, finish) t
      ON t.scryfall_id = h.scryfall_id AND t.finish = h.finish AND t.chosen = h.as_of`

const newestObservation = `MAX(as_of)`

const windowBaseline = `COALESCE(MAX(CASE WHEN as_of <= ? THEN as_of END), MIN(as_of))`

const paidByPriceFinish = `
    SELECT e.scryfall_id AS sid,
           CASE WHEN e.finish = 'etched' AND c.price_usd_etched IS NOT NULL
                     THEN 'etched'
                WHEN e.finish IN ('foil','etched') THEN 'foil'
                WHEN e.finish = 'nonfoil' THEN 'nonfoil' END AS pfinish,
           SUM(` + countedQuantity + `) AS copies,
           SUM(e.purchase_price * ` + countedQuantity + `) AS spent
    FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
    JOIN containers ctc ON ctc.id = e.container_id
    WHERE e.purchase_price IS NOT NULL
    GROUP BY sid, pfinish
    HAVING copies > 0`

func (s *Store) HasCostBasis() (bool, error) {
	var found int
	err := s.db.QueryRow(`
SELECT EXISTS(SELECT 1 FROM card_entries WHERE purchase_price IS NOT NULL)`).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("looking for a cost basis: %w", err)
	}
	return found == 1, nil
}

func (s *Store) CostBasisMovers() ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH paid AS (` + paidByPriceFinish + `),
     cur AS (` + fmt.Sprintf(pricesAt, newestObservation) + `)
SELECT c.scryfall_id, cur.pfinish, c.name, c.set_code, c.collector_number,
       p.copies, p.spent / p.copies, cur.price, cur.source, c.color_identity,
       c.promo_types, COALESCE(c.lang, ''), COALESCE(c.released_at, '')
FROM paid p
JOIN cur ON cur.sid = p.sid AND cur.pfinish = p.pfinish
JOIN cards c ON c.scryfall_id = p.sid`)
	if err != nil {
		return nil, fmt.Errorf("reading cost basis: %w", err)
	}
	defer rows.Close()

	var out []PriceChange
	for rows.Next() {
		var p PriceChange
		var colors, promos sql.NullString
		if err := rows.Scan(&p.ScryfallID, &p.Finish, &p.Name, &p.SetCode,
			&p.CollectorNumber, &p.Copies, &p.Old, &p.New, &p.Source, &colors,
			&promos, &p.Lang, &p.ReleasedAt); err != nil {
			return nil, err
		}
		p.ColorIdentity = parseColorIdentity(colors)
		p.Treatment = FoilTreatment(promos)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RecordPrices() ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH eff AS (` + effectivePrices + `),
     owned AS (` + ownedByPriceFinish + `),
     latest AS (` + fmt.Sprintf(pricesAt, newestObservation) + `)
SELECT e.sid, e.pfinish, e.price, e.source, c.name, c.set_code, c.collector_number,
       COALESCE(c.released_at, ''), COALESCE(o.copies, 0), l.price
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
			&p.SetCode, &p.CollectorNumber, &p.ReleasedAt, &p.Copies, &old); err != nil {
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

	ts := now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := appendPrices(tx, seen, ts); err != nil {
		return nil, err
	}

	if err := snapshotValue(tx, ts); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return moved, nil
}

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
JOIN containers ct ON ct.id = e.container_id AND ct.counted = 1
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

type ValuePoint struct {
	AsOf   string
	Binder float64
	Decks  float64
	Total  float64
	Seeded bool
}

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

func appendPrices(tx *sql.Tx, changes []PriceChange, ts string) error {
	if len(changes) == 0 {
		return nil
	}

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

func (s *Store) Movers(since string) ([]PriceChange, error) {
	rows, err := s.db.Query(`
WITH owned AS (`+ownedByPriceFinish+`),
     cur AS (`+fmt.Sprintf(pricesAt, newestObservation)+`),
     base AS (`+fmt.Sprintf(pricesAt, windowBaseline)+`)
SELECT c.scryfall_id, cur.pfinish, c.name, c.set_code, c.collector_number,
       o.copies, base.price, cur.price, cur.source, c.color_identity, c.promo_types,
       COALESCE(c.lang, ''), COALESCE(c.released_at, ''), base.as_of
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
			&p.Lang, &p.ReleasedAt, &p.OldAsOf); err != nil {
			return nil, err
		}
		p.ColorIdentity = parseColorIdentity(colors)
		p.Treatment = FoilTreatment(promos)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PriceHistoryDepth() (observations int, oldest string, err error) {
	var first sql.NullString
	err = s.db.QueryRow(`SELECT COUNT(*), MIN(as_of) FROM card_price_history`).
		Scan(&observations, &first)
	if err != nil {
		return 0, "", err
	}
	return observations, first.String, nil
}

func backfillStamp(date string) string { return date + "T00:00:00Z" }

func (s *Store) BackfillPrices(byCard map[string][]mtgjson.Observation) (inserted, cards int, err error) {
	return s.backfillHistory("card_price_history", byCard)
}

func (s *Store) backfillHistory(table string, byCard map[string][]mtgjson.Observation) (inserted, cards int, err error) {
	if len(byCard) == 0 {
		return 0, 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if err := retireSwitchedSeries(tx, table, byCard); err != nil {
		return 0, 0, err
	}

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

	printed, err := printedFinishes(tx)
	if err != nil {
		return 0, 0, err
	}

	for sid, obs := range byCard {
		var wrote bool
		bound := func(f finish.Finish) string { return firstLive[sid+"|"+f.String()] }
		for _, o := range compactSeries(obs, bound) {
			if f, known := printed[sid]; known && !f[o.Finish.String()] {
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

func retireSwitchedSeries(tx *sql.Tx, table string, byCard map[string][]mtgjson.Observation) error {
	incoming := map[string]string{}
	for sid, obs := range byCard {
		for _, o := range obs {
			if o.Source != "" {
				incoming[sid+"|"+o.Finish.String()] = o.Source
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

func compactSeries(obs []mtgjson.Observation, before func(finish.Finish) string) []mtgjson.Observation {
	day := func(ts string) string {
		if len(ts) > len("2006-01-02") {
			return ts[:len("2006-01-02")]
		}
		return ts
	}
	byFinish := map[finish.Finish][]mtgjson.Observation{}
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
			continue
		}
		set := make(map[string]bool, len(finishes)+1)
		for _, f := range finishes {
			set[f] = true
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
