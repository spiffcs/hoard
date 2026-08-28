package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

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

func (r TrendRow) dipping() bool {
	return r.Last <= r.Low*dipFloorBand && r.Last < r.High*dipDrawdown
}

func (r TrendRow) climbing() bool {
	return r.Downs == 0 && r.Moves > 0 && r.Last > r.First
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

const trendAggregate = `
      SELECT sid, fin, COUNT(*) AS n, MIN(px) AS lo, MAX(px) AS hi,
             MIN(first_px) AS first_px, MIN(last_px) AS last_px,
             SUM(CASE WHEN prev IS NOT NULL AND px > prev THEN 1 ELSE 0 END) AS ups,
             SUM(CASE WHEN prev IS NOT NULL AND px < prev THEN 1 ELSE 0 END) AS downs,
             SUM(CASE WHEN prev IS NOT NULL THEN 1 ELSE 0 END) AS moves
      FROM (SELECT h.scryfall_id AS sid, h.finish AS fin, h.price_usd AS px,
                   LAG(h.price_usd) OVER w AS prev,
                   FIRST_VALUE(h.price_usd) OVER wf AS first_px,
                   LAST_VALUE(h.price_usd) OVER wf AS last_px
            FROM card_price_history h
            WHERE h.as_of >= ?
            WINDOW w AS (PARTITION BY h.scryfall_id, h.finish ORDER BY h.as_of),
                   wf AS (PARTITION BY h.scryfall_id, h.finish ORDER BY h.as_of
                          ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING))
      GROUP BY sid, fin`

const fillTrendStats = `
INSERT OR REPLACE INTO card_trend_stats
  (since, built_day, scryfall_id, finish, n, lo, hi, first_px, last_px, ups, downs, moves)
SELECT ?, ?, sid, fin, n, lo, hi, first_px, last_px, ups, downs, moves
FROM (` + trendAggregate + `)`

const trendFingerprintKey = "trends.fingerprint"

const trendWriterWait = 250 * time.Millisecond

var dipTest = `last_px <= lo * ` + fmt.Sprint(dipFloorBand) +
	` AND last_px < hi * ` + fmt.Sprint(dipDrawdown)

var momentumTest = `downs = 0 AND moves > 0 AND last_px > first_px`

var trendKeep = ` AND n >= ? AND last_px >= ?` +
	` AND ((` + dipTest + `) OR (` + momentumTest + `))`

var trendStatsSource = `
      SELECT scryfall_id AS sid, finish AS fin, n, lo, hi, first_px, last_px, ups, downs, moves
      FROM card_trend_stats WHERE since = ?` + trendKeep

var trendLiveSource = `
      SELECT sid, fin, n, lo, hi, first_px, last_px, ups, downs, moves
      FROM (` + trendAggregate + `) WHERE 1 = 1` + trendKeep

var trendRowSelect = `
SELECT c.scryfall_id, c.name, c.set_code, c.collector_number, e.fin, o.copies,
       e.first_px, e.last_px, e.lo, e.hi, e.ups, e.downs, e.moves,
       c.color_identity, c.promo_types, COALESCE(c.lang, ''), COALESCE(c.released_at, '')
FROM (%s) e
JOIN owned o ON o.sid = e.sid AND o.pfinish = e.fin
JOIN cards c ON c.scryfall_id = e.sid
WHERE ? = '' OR COALESCE(c.released_at, '') < ?`

func trendQuery(source string) string {
	return `
WITH owned AS (` + ownedByPriceFinish + `)` + fmt.Sprintf(trendRowSelect, source)
}

func (s *Store) historyFingerprint() (string, error) {
	var newest sql.NullInt64
	var rows int64
	if err := s.reads().QueryRow(
		`SELECT MAX(rowid), COUNT(*) FROM card_price_history`).Scan(&newest, &rows); err != nil {
		return "", fmt.Errorf("fingerprinting the price history: %w", err)
	}
	return fmt.Sprintf("%d|%d", newest.Int64, rows), nil
}

func (s *Store) trendStatsWarm(since, fingerprint string) (bool, error) {
	var stored sql.NullString
	err := s.reads().QueryRow(
		`SELECT value FROM settings WHERE key = ?`, trendFingerprintKey).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("reading the trend fingerprint: %w", err)
	}
	if stored.String != fingerprint {
		return false, nil
	}
	var n int
	if err := s.reads().QueryRow(
		`SELECT COUNT(*) FROM card_trend_stats WHERE since = ?`, since).Scan(&n); err != nil {
		return false, fmt.Errorf("checking the trend stats: %w", err)
	}
	return n > 0, nil
}

func (s *Store) buildTrendStats(since, fingerprint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), trendWriterWait)
	conn, err := s.db.Conn(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("waiting for the writer: %w", err)
	}
	defer conn.Close()

	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	day := now()[:len("2006-01-02")]
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM card_trend_stats WHERE built_day <> ?`, []any{day}},
		{fillTrendStats, []any{since, day, since}},
		{`INSERT INTO settings (key, value) VALUES (?, ?)
          ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			[]any{trendFingerprintKey, fingerprint}},
	} {
		if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
			return fmt.Errorf("building trend stats: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) clearTrendStats(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM card_trend_stats`); err != nil {
		return fmt.Errorf("clearing trend stats: %w", err)
	}
	return nil
}

func (s *Store) Trends(o TrendOptions) (dips, momentum []TrendRow, err error) {
	checks := o.MinChecks
	if checks < 2 {
		checks = 2
	}
	source := trendLiveSource
	if fingerprint, err := s.historyFingerprint(); err == nil {
		warm, err := s.trendStatsWarm(o.Since, fingerprint)
		if err == nil && !warm {
			warm = s.buildTrendStats(o.Since, fingerprint) == nil
		}
		if warm {
			source = trendStatsSource
		}
	}

	rows, err := s.reads().Query(trendQuery(source),
		o.Since, checks, o.MinPrice, o.SettledBefore, o.SettledBefore)
	if err != nil {
		return nil, nil, fmt.Errorf("reading price trends: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r TrendRow
		var colors, promos sql.NullString
		if err := rows.Scan(&r.ScryfallID, &r.Name, &r.SetCode, &r.CollectorNumber,
			&r.Finish, &r.Copies, &r.First, &r.Last, &r.Low, &r.High,
			&r.Ups, &r.Downs, &r.Moves, &colors, &promos, &r.Lang, &r.ReleasedAt); err != nil {
			return nil, nil, err
		}
		r.ColorIdentity = parseColorIdentity(colors)
		r.Treatment = FoilTreatment(promos)
		if r.dipping() {
			dips = append(dips, r)
		}
		if r.climbing() {
			momentum = append(momentum, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	slices.SortStableFunc(dips, func(a, b TrendRow) int {
		if c := cmp.Compare(a.OffHigh(), b.OffHigh()); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.SortStableFunc(momentum, func(a, b TrendRow) int {
		if c := cmp.Compare(b.Moves, a.Moves); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Change(), a.Change()); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return dips, momentum, nil
}

func (s *Store) Dips(o TrendOptions) ([]TrendRow, error) {
	dips, _, err := s.Trends(o)
	return dips, err
}

func (s *Store) Momentum(o TrendOptions) ([]TrendRow, error) {
	_, momentum, err := s.Trends(o)
	return momentum, err
}

func TrendExtent(rows []TrendRow) float64 {
	var extent float64
	for _, r := range rows {
		extent = max(extent, math.Abs(r.Change()))
	}
	return extent
}
