package store

import (
	"database/sql"
	"fmt"
	"math"

	"github.com/spiffcs/hoard/internal/finish"
)

type TrendOptions struct {
	Since         string
	SettledBefore string
	MinPrice      float64
	MinChecks     int
}

type TrendRow struct {
	ScryfallID      string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          finish.Finish
	Copies          int

	First, Last float64
	Low, High   float64

	Ups, Downs, Moves int

	ColorIdentity []string
	Treatment     string
	Lang          string
	ReleasedAt    string
}

func (r TrendRow) OffHigh() float64 {
	if r.High <= 0 {
		return 0
	}
	return r.Last/r.High - 1
}

func (r TrendRow) Change() float64 {
	if r.First <= 0 {
		return 0
	}
	return r.Last/r.First - 1
}

const dipFloorBand = 1.03

const dipDrawdown = 0.85

const trendWindow = `
    seq AS (
      SELECT h.scryfall_id AS sid, h.finish AS fin, h.as_of, h.price_usd AS px,
             LAG(h.price_usd) OVER (PARTITION BY h.scryfall_id, h.finish
                                    ORDER BY h.as_of) AS prev
      FROM card_price_history h
      WHERE h.as_of >= ?),
    agg AS (
      SELECT sid, fin, COUNT(*) AS n, MIN(px) AS lo, MAX(px) AS hi,
             MIN(as_of) AS fa, MAX(as_of) AS la,
             SUM(CASE WHEN prev IS NOT NULL AND px > prev THEN 1 ELSE 0 END) AS ups,
             SUM(CASE WHEN prev IS NOT NULL AND px < prev THEN 1 ELSE 0 END) AS downs,
             SUM(CASE WHEN prev IS NOT NULL THEN 1 ELSE 0 END) AS moves
      FROM seq GROUP BY sid, fin),
    ends AS (
      SELECT a.sid, a.fin, a.n, a.lo, a.hi, a.ups, a.downs, a.moves,
             fp.px AS first_px, lp.px AS last_px
      FROM agg a
      JOIN seq fp ON fp.sid = a.sid AND fp.fin = a.fin AND fp.as_of = a.fa
      JOIN seq lp ON lp.sid = a.sid AND lp.fin = a.fin AND lp.as_of = a.la)`

const trendSelect = `
SELECT c.scryfall_id, c.name, c.set_code, c.collector_number, e.fin, o.copies,
       e.first_px, e.last_px, e.lo, e.hi, e.ups, e.downs, e.moves,
       c.color_identity, c.promo_types, COALESCE(c.lang, ''), COALESCE(c.released_at, '')
FROM ends e
JOIN owned o ON o.sid = e.sid AND o.pfinish = e.fin
JOIN cards c ON c.scryfall_id = e.sid
WHERE e.n >= ? AND e.last_px >= ?
  AND (? = '' OR COALESCE(c.released_at, '') < ?)`

func (s *Store) Dips(o TrendOptions) ([]TrendRow, error) {
	return s.trends(o, `
      AND e.last_px <= e.lo * `+fmt.Sprint(dipFloorBand)+`
      AND e.last_px < e.hi * `+fmt.Sprint(dipDrawdown)+`
ORDER BY e.last_px / e.hi ASC, c.name`, "dips")
}

func (s *Store) Momentum(o TrendOptions) ([]TrendRow, error) {
	return s.trends(o, `
      AND e.downs = 0 AND e.moves > 0 AND e.last_px > e.first_px
ORDER BY e.moves DESC, e.last_px / e.first_px DESC, c.name`, "momentum")
}

func (s *Store) trends(o TrendOptions, tail, what string) ([]TrendRow, error) {
	checks := o.MinChecks
	if checks < 2 {
		checks = 2
	}
	rows, err := s.reads().Query(`
WITH owned AS (`+ownedByPriceFinish+`),`+trendWindow+trendSelect+tail,
		o.Since, checks, o.MinPrice, o.SettledBefore, o.SettledBefore)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", what, err)
	}
	defer rows.Close()

	var out []TrendRow
	for rows.Next() {
		var r TrendRow
		var colors, promos sql.NullString
		if err := rows.Scan(&r.ScryfallID, &r.Name, &r.SetCode, &r.CollectorNumber,
			&r.Finish, &r.Copies, &r.First, &r.Last, &r.Low, &r.High,
			&r.Ups, &r.Downs, &r.Moves, &colors, &promos, &r.Lang, &r.ReleasedAt); err != nil {
			return nil, err
		}
		r.ColorIdentity = parseColorIdentity(colors)
		r.Treatment = FoilTreatment(promos)
		out = append(out, r)
	}
	return out, rows.Err()
}

func TrendExtent(rows []TrendRow) float64 {
	var extent float64
	for _, r := range rows {
		extent = max(extent, math.Abs(r.Change()))
	}
	return extent
}
