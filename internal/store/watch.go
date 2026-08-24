package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
)

type Watch struct {
	ID         int64
	ScryfallID string

	Display   string
	Finish    finish.Finish
	Op        string
	Threshold float64

	Pct float64

	MinMove float64

	WindowDays  int
	CreatedAt   string
	LastState   string
	LastFiredAt string
}

type WatchStatus struct {
	Watch
	Name            string
	SetCode         string
	CollectorNumber string
	MTGJSONUUID     string

	Treatment string

	Lang string

	ColorIdentity []string

	PriceUSD *float64

	Anchor *float64

	AnchorAt string

	HistorySince string
	WindowFrom   string
}

func (w WatchStatus) WaitingOnHistory() bool {
	if w.Op != "drop" && w.Op != "rise" {
		return false
	}
	if w.Anchor == nil || w.HistorySince == "" {
		return true
	}
	return w.HistorySince > w.WindowFrom
}

func (w WatchStatus) Met() bool {
	if w.PriceUSD == nil {
		return false
	}
	switch w.Op {
	case "under":
		return *w.PriceUSD < w.Threshold
	case "over":
		return *w.PriceUSD > w.Threshold
	case "drop":
		if w.WaitingOnHistory() {
			return false
		}
		return *w.PriceUSD < *w.Anchor*(1-w.Pct) && *w.Anchor-*w.PriceUSD >= w.MinMove
	case "rise":
		if w.WaitingOnHistory() {
			return false
		}
		return *w.PriceUSD > *w.Anchor*(1+w.Pct) && *w.PriceUSD-*w.Anchor >= w.MinMove
	}
	return false
}

func (w WatchStatus) State() string {
	switch {
	case w.PriceUSD == nil:
		return "unpriced"

	case w.WaitingOnHistory():
		return "waiting-on-history"
	case w.Met():
		return "met"
	default:
		return "waiting"
	}
}

func (w WatchStatus) WouldFire() bool {
	return w.PriceUSD != nil && w.Met() && w.LastState != "met"
}

func (w Watch) Percent() bool { return w.Op == "drop" || w.Op == "rise" }

func (w Watch) Rule() string {
	if w.Percent() {
		return fmt.Sprintf("%s %s%%", w.Op, strconv.FormatFloat(w.Pct*100, 'f', -1, 64))
	}
	return fmt.Sprintf("%s $%.2f", w.Op, w.Threshold)
}

const watchUpsertSQL = `
INSERT INTO watches (scryfall_id, display, finish, op, threshold,
                     pct, min_move, window_days, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, op) DO UPDATE SET
    threshold     = excluded.threshold,
    pct           = excluded.pct,
    min_move      = excluded.min_move,
    window_days   = excluded.window_days,
    display       = excluded.display,
    last_state    = '',
    last_fired_at = ''`

const DefaultWindowDays = 30

const DefaultMinMove = 0.25

func ParsePercent(op, raw string) (float64, error) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("want a percentage like 10 or 10%%, not %q", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf(
			"%s is %g%% — a percentage, not a fraction, so ten percent is written 10", raw, n)
	}
	if op == "drop" && n >= 100 {
		return 0, fmt.Errorf(
			"%s asks about the price falling to nothing; a drop is a percentage below 100", raw)
	}
	return n / 100, nil
}

func validateWatch(w WatchInput) error {
	switch w.Op {
	case "under", "over":
		if w.Pct != 0 {
			return fmt.Errorf("a %s watch is a dollar threshold and takes no percentage", w.Op)
		}
	case "drop", "rise":
		if w.Threshold != 0 {
			return fmt.Errorf("a %s watch is a movement and takes no dollar threshold", w.Op)
		}
		if w.Pct <= 0 || w.Pct >= 1 {
			return fmt.Errorf("a %s watch needs a percentage above 0 and below 100, not %g", w.Op, w.Pct*100)
		}
		if w.MinMove < 0 {
			return fmt.Errorf("a %s watch cannot have a negative minimum move", w.Op)
		}
		if w.WindowDays <= 0 {
			return fmt.Errorf("a %s watch needs a window of at least a day", w.Op)
		}
	default:
		return fmt.Errorf("watch op must be under, over, drop or rise, not %q", w.Op)
	}
	if err := validFinish(w.Finish); err != nil {
		return fmt.Errorf("watch %v", err)
	}
	return nil
}

func (s *Store) AddWatch(scryfallID, display string, fin finish.Finish, op string, threshold float64) error {
	return s.AddWatchInput(WatchInput{ScryfallID: scryfallID, Display: display,
		Finish: fin, Op: op, Threshold: threshold})
}

func (s *Store) AddWatchInput(w WatchInput) error {
	w.normalize()
	if err := validateWatch(w); err != nil {
		return err
	}
	if _, err := s.db.Exec(watchUpsertSQL, w.ScryfallID, w.Display, w.Finish, w.Op,
		w.Threshold, w.Pct, w.MinMove, w.WindowDays, now()); err != nil {
		return fmt.Errorf("recording watch: %w", err)
	}
	return nil
}

type WatchInput struct {
	ScryfallID string
	Display    string
	Finish     finish.Finish
	Op         string
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
}

func (w *WatchInput) normalize() {
	if w.WindowDays <= 0 {
		w.WindowDays = DefaultWindowDays
	}
}

func (s *Store) AddWatches(ws []WatchInput) (created, updated int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, w := range ws {
		w.normalize()
		if err := validateWatch(w); err != nil {
			return 0, 0, err
		}
		var exists bool
		if err := tx.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM watches WHERE scryfall_id = ? AND finish = ? AND op = ?)`,
			w.ScryfallID, w.Finish, w.Op).Scan(&exists); err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(watchUpsertSQL, w.ScryfallID, w.Display, w.Finish, w.Op,
			w.Threshold, w.Pct, w.MinMove, w.WindowDays, now()); err != nil {
			return 0, 0, fmt.Errorf("recording watch: %w", err)
		}
		if exists {
			updated++
		} else {
			created++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return created, updated, nil
}

const effSourceExpr = `
    CASE WHEN w.finish = 'etched' THEN
              CASE WHEN o.price_usd_etched IS NOT NULL THEN o.source
                   WHEN c.price_usd_etched IS NOT NULL THEN 'scryfall'
                   WHEN o.price_usd_foil IS NOT NULL THEN o.source
                   WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd_foil, '') END
         WHEN w.finish = 'foil' THEN
              CASE WHEN o.price_usd_foil IS NOT NULL THEN o.source
                   WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd_foil, '') END
         WHEN w.finish = 'nonfoil' THEN
              CASE WHEN o.price_usd IS NOT NULL THEN o.source
                   WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd, '') END END`

const anchorSeries = `
        FROM card_price_history h
       WHERE h.scryfall_id = ws.scryfall_id
         AND h.finish      = ws.finish
         AND h.source      = ws.eff_source`

const anchorFrom = `
    COALESCE((SELECT MAX(h.as_of) ` + anchorSeries + `
         AND h.as_of <= ws.anchor_lower), ws.anchor_lower)`

const watchBounds = `
WITH bounds AS (
    SELECT watches.*,
           strftime('%Y-%m-%dT%H:%M:%SZ', ?, '-' || window_days || ' days') AS window_from
      FROM watches
), w AS (
    SELECT bounds.*,
           MAX(window_from, created_at, last_fired_at) AS anchor_lower
      FROM bounds
)`

const watchStatusQuery = watchBounds + `,
ws AS (
    SELECT w.*,
           c.name AS card_name, c.set_code, c.collector_number,
           COALESCE(c.mtgjson_uuid, '') AS uuid, c.promo_types,
           COALESCE(c.lang, '') AS card_lang, c.color_identity,
           CASE WHEN w.finish = 'etched' THEN ` + effPriceEtched + `
                WHEN w.finish = 'foil'    THEN ` + effPriceFoil + `
                WHEN w.finish = 'nonfoil' THEN ` + effPriceUSD + ` END AS price,
           ` + effSourceExpr + ` AS eff_source,
           COALESCE((SELECT MIN(h.as_of) FROM card_price_history h
                      WHERE h.scryfall_id = w.scryfall_id
                        AND h.finish = w.finish), '') AS history_since
      FROM w
      JOIN cards c ON c.scryfall_id = w.scryfall_id
      ` + altJoinCards + `
)
SELECT ws.id, ws.scryfall_id, ws.display, ws.finish, ws.op, ws.threshold,
       ws.pct, ws.min_move, ws.window_days, ws.created_at, ws.last_state,
       ws.last_fired_at,
       ws.card_name, ws.set_code, ws.collector_number, ws.uuid,
       ws.promo_types, ws.card_lang, ws.color_identity, ws.price,
       CASE WHEN ws.op IN ('drop','rise') THEN
                 (SELECT CASE WHEN ws.op = 'drop' THEN MAX(h.price_usd)
                              ELSE MIN(h.price_usd) END ` + anchorSeries + `
                    AND h.as_of >= ` + anchorFrom + `) END,
       COALESCE((SELECT h.as_of ` + anchorSeries + `
                   AND h.as_of >= ` + anchorFrom + `
                 ORDER BY CASE WHEN ws.op = 'drop' THEN -h.price_usd
                               ELSE h.price_usd END, h.as_of LIMIT 1), ''),
       ws.history_since, ws.window_from
FROM ws
ORDER BY ws.display, ws.finish, ws.op, ws.id`

func (s *Store) ListWatches() ([]WatchStatus, error) { return s.listWatchesAt(now()) }

func (s *Store) listWatchesAt(at string) ([]WatchStatus, error) {
	rows, err := s.db.Query(watchStatusQuery, at)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	defer rows.Close()
	var out []WatchStatus
	for rows.Next() {
		var w WatchStatus
		var promos, colors sql.NullString
		if err := rows.Scan(&w.ID, &w.ScryfallID, &w.Display, &w.Finish, &w.Op,
			&w.Threshold, &w.Pct, &w.MinMove, &w.WindowDays, &w.CreatedAt,
			&w.LastState, &w.LastFiredAt,
			&w.Name, &w.SetCode, &w.CollectorNumber, &w.MTGJSONUUID,
			&promos, &w.Lang, &colors, &w.PriceUSD,
			&w.Anchor, &w.AnchorAt, &w.HistorySince, &w.WindowFrom); err != nil {
			return nil, err
		}
		w.Treatment = FoilTreatment(promos)
		w.ColorIdentity = parseColorIdentity(colors)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) CheckWatches() (fired []WatchStatus, checked int, err error) {
	return s.checkWatchesAt(now())
}

func (s *Store) checkWatchesAt(ts string) (fired []WatchStatus, checked int, err error) {
	all, err := s.listWatchesAt(ts)
	if err != nil {
		return nil, 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE watches SET last_state = ? WHERE id = ?`)
	if err != nil {
		return nil, 0, err
	}
	defer stmt.Close()

	refire, err := tx.Prepare(`UPDATE watches SET last_state = ?, last_fired_at = ? WHERE id = ?`)
	if err != nil {
		return nil, 0, err
	}
	defer refire.Close()

	for _, w := range all {
		if w.PriceUSD == nil {
			continue
		}
		checked++
		state := "unmet"
		if w.Met() {
			state = "met"
		}
		if state == "met" && w.LastState != "met" {
			fired = append(fired, w)
			if _, err := refire.Exec(state, ts, w.ID); err != nil {
				return nil, 0, fmt.Errorf("updating watch state: %w", err)
			}
			continue
		}
		if state != w.LastState {
			if _, err := stmt.Exec(state, w.ID); err != nil {
				return nil, 0, fmt.Errorf("updating watch state: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return fired, checked, nil
}

func (s *Store) WatchByRef(ref string) (WatchStatus, error) {
	all, err := s.ListWatches()
	if err != nil {
		return WatchStatus{}, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, w := range all {
			if w.ID == id {
				return w, nil
			}
		}
		return WatchStatus{}, fmt.Errorf("no watch with id %d", id)
	}
	var matches []WatchStatus
	for _, w := range all {
		if strings.Contains(strings.ToLower(w.Display), strings.ToLower(ref)) {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 0:
		return WatchStatus{}, fmt.Errorf("no watch matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, w := range matches {
			names[i] = fmt.Sprintf("%d: %s %s %s", w.ID, w.Display, w.Finish, w.Rule())
		}
		return WatchStatus{}, fmt.Errorf("%q matches %d watches:\n  %s\nuse the id",
			ref, len(matches), strings.Join(names, "\n  "))
	}
}

func (s *Store) HasAnchorSeries(scryfallID string, fin finish.Finish) (bool, error) {
	var ok bool
	err := s.db.QueryRow(`
WITH w AS (SELECT ? AS scryfall_id, ? AS finish)
SELECT EXISTS(
    SELECT 1 FROM card_price_history h, w
     JOIN cards c ON c.scryfall_id = w.scryfall_id
     `+altJoinCards+`
     WHERE h.scryfall_id = w.scryfall_id
       AND h.finish      = w.finish
       AND h.source      = `+effSourceExpr+`)`,
		scryfallID, fin).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("checking price history: %w", err)
	}
	return ok, nil
}

type HeldPrinting struct {
	ScryfallID      string
	SetCode         string
	CollectorNumber string
	Copies          int
	AnyCopies       int
}

func (s *Store) HeldPrintingsOfName(name string, fin finish.Finish) ([]HeldPrinting, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number,
       COALESCE(SUM(CASE WHEN e.finish = ? THEN e.quantity END), 0) AS copies,
       COALESCE(SUM(e.quantity), 0) AS any_copies
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE c.name = ? COLLATE NOCASE
GROUP BY c.scryfall_id, c.set_code, c.collector_number
ORDER BY copies DESC, any_copies DESC, c.set_code, c.collector_number`, fin, name)
	if err != nil {
		return nil, fmt.Errorf("reading held printings of %q: %w", name, err)
	}
	defer rows.Close()
	var out []HeldPrinting
	for rows.Next() {
		var h HeldPrinting
		if err := rows.Scan(&h.ScryfallID, &h.SetCode, &h.CollectorNumber,
			&h.Copies, &h.AnyCopies); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) RemoveWatch(id int64) error {
	res, err := s.db.Exec(`DELETE FROM watches WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("removing watch: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no watch with id %d", id)
	}
	return nil
}

func (s *Store) WouldFire() ([]WatchStatus, error) { return s.wouldFireAt(now()) }

func (s *Store) wouldFireAt(ts string) ([]WatchStatus, error) {
	all, err := s.listWatchesAt(ts)
	if err != nil {
		return nil, err
	}
	var fired []WatchStatus
	for _, w := range all {
		if w.WouldFire() {
			fired = append(fired, w)
		}
	}
	return fired, nil
}
