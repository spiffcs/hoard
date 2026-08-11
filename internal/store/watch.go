package store

// Price watches: standing thresholds on individual printings, checked
// against stored prices. The intended pairing is a cron's
// `hoard update-prices && hoard watch` — the first fetches reality, the
// second answers "did anything I care about cross a line?".

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Watch is one standing alert on one printing and finish.
//
// Op names both the question and its units. under and over are dollar lines,
// strict in both directions — under 30 means the price fell below $30.00, not
// to it — and read Threshold. drop and rise are movements and read Pct: a drop
// watch fires when the price falls Pct below the highest price the printing
// reached inside its window. The vocabulary is split rather than shared
// because "under" names a place and "drop" names a movement, and a single word
// covering both is what would force watch list to explain that "over 10"
// sometimes means dollars.
//
// LastState remembers the previous check ("met", "unmet", or the empty string
// before the first one), which is what makes an alert fire on the crossing
// rather than on every check it sits past its condition. LastFiredAt is the
// moment the alert last went out, and is the third lower bound on the anchor
// query: firing re-anchors a trailing watch, which is what collapses one
// continuous slide into one alert rather than one per bounce.
type Watch struct {
	ID         int64
	ScryfallID string
	// Display is the card's name as resolved at creation; the set, number
	// and price are joined from the catalog at read time.
	Display   string
	Finish    string // nonfoil|foil|etched — the finishes prices come in
	Op        string // under|over|drop|rise
	Threshold float64
	// Pct is the movement that fires a drop or rise, as a fraction: 0.1 is ten
	// percent. A fraction rather than 10, decided once and at the boundary, so
	// the store never sees a percent sign and no read has to guess a factor of
	// a hundred.
	Pct float64
	// MinMove is the smallest movement in dollars worth alerting on. Ten
	// percent of a fifty cent card is a nickel, and without a floor the
	// collection generates alerts by the hundred — see the DefaultMinMove
	// measurement.
	MinMove float64
	// WindowDays is how far back the anchor looks for its extreme.
	WindowDays  int
	CreatedAt   string
	LastState   string
	LastFiredAt string
}

// WatchStatus is a watch beside what the catalog currently knows about its
// card. Met is meaningful only when PriceUSD is non-nil: an unpriced card
// answers none of the four questions.
type WatchStatus struct {
	Watch
	Name            string
	SetCode         string
	CollectorNumber string
	MTGJSONUUID     string
	// Treatment is the foil treatment's display word, empty for plain —
	// same semantics as Card.Treatment.
	Treatment string
	// Lang is the printing's language code ("en", "ja"), empty when the card's
	// document has not been stored — same semantics as Card.Lang.
	Lang string
	// PriceUSD is hoard's effective price, the same figure every other screen
	// shows. A percent watch compares it against an anchor drawn from the
	// series that produced it — see effSourceExpr — so both ends of the
	// movement come from one vendor without the row having to show a number
	// the rest of hoard disagrees with.
	PriceUSD *float64
	// Anchor is the extreme price the movement is measured from: the highest
	// for a drop, the lowest for a rise, over the watch's window. Nil for an
	// absolute watch, and nil for a percent watch on a printing with no
	// observations at all, which answers neither direction.
	Anchor *float64
	// AnchorAt is when that extreme was observed (RFC 3339), so an alert can
	// say "down 11% from its 3 July high" rather than asserting a movement
	// with nothing to check it against.
	AnchorAt string
	// HistorySince is the printing's oldest observation from any source, and
	// WindowFrom is the window's plain lower bound. Together they answer
	// whether the record reaches back far enough for the anchor to mean
	// anything — see WaitingOnHistory.
	HistorySince string
	WindowFrom   string
}

// WaitingOnHistory reports that a percent watch cannot honestly answer yet
// because the series it anchors on is younger than the window it summarises.
//
// The guard is that the record must reach back to the window's start, and it
// takes its threshold from the watch's own window rather than from a number
// picked by hand: a thirty-day high cannot be read off a series that has only
// existed for seven days, whatever the number of rows in it.
//
// A count of observations would be the wrong guard and the measurement says
// so. card_price_history is a change log, so a stable printing writes almost
// nothing — Talon Gates of Madara has five observations because its price has
// not moved since 12 May, not because it is unobserved, and muting it for
// thinness would mute the best-known prices in the collection. Measured
// against the owner's database, a count of five would have silenced 63% of
// held series while this guard silences 5 of 1,968.
//
// The reach is the *printing's*, across every source, not the anchored series'
// alone. That distinction is worth 83% of the collection: the anchored series
// is whichever vendor is currently answering, and when the feed changed over
// in late July the new vendor's slice was eleven days old, so a slice-based
// guard muted 1,652 of 1,968 held series for a reason that has nothing to do
// with how well the price is known. It would also have been guarding the wrong
// direction. A window truncated to a young slice puts the anchor *nearer* the
// current price, which makes both a drop and a rise harder to reach, not
// easier — §2.7 measured exactly that, where seven- and fourteen-day windows
// fire zero times on the owner's nine printings. A short slice fails closed on
// its own; a genuinely new printing, whose first recorded price may be a
// preorder spike, is the case that needs the guard, and that is what the
// printing's own reach detects.
func (w WatchStatus) WaitingOnHistory() bool {
	if w.Op != "drop" && w.Op != "rise" {
		return false
	}
	if w.Anchor == nil || w.HistorySince == "" {
		return true
	}
	return w.HistorySince > w.WindowFrom
}

// Met reports whether the watch's condition currently holds.
//
// It stays a pure function of the row plus the prices joined onto it — the
// anchor is a query, but watchStatusQuery runs it, so no caller has to hand
// this method a database.
//
// An unknown op answers false rather than panicking: a database written by a
// newer hoard can carry a direction this binary has never heard of, and a
// watch that stays quiet is a better failure than one that crashes the cron
// the alerts are delivered by.
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

// Percent reports whether the watch measures a movement rather than a line.
func (w Watch) Percent() bool { return w.Op == "drop" || w.Op == "rise" }

// Rule states the watch's question in one phrase — "over $149.86", "drop 10%"
// — for the places that have to name a watch without a table to lay it out in.
//
// It reads threshold only for the ops whose units are dollars. A single
// column carrying both would have printed "$0.10" for a ten percent watch
// here and in five other places, and $0.10 is a plausible enough threshold
// that nobody would have caught it.
func (w Watch) Rule() string {
	if w.Percent() {
		return fmt.Sprintf("%s %s%%", w.Op, strconv.FormatFloat(w.Pct*100, 'f', -1, 64))
	}
	return fmt.Sprintf("%s $%.2f", w.Op, w.Threshold)
}

// watchUpsertSQL replaces any existing watch on the same card, finish and
// direction, resetting both LastState and LastFiredAt: the new rule has not
// been checked yet, and the moment the *old* rule last fired is not a bound
// the new one may anchor from. Re-adding therefore re-arms a percent watch,
// which is the same contract adjusting a threshold has always had — a question
// restated is a question not yet answered, and hoard's rule is that a
// condition already met is worth exactly one alert.
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

// DefaultWindowDays is how far back a percent watch looks for its extreme,
// matching `hoard movers --since 30d`.
//
// Thirty rather than ninety because a stale high ages out of a thirty-day
// window, while a window long enough to reach a watch's creation turns a
// trailing anchor back into a pinned one — the exact failure percent watches
// exist to avoid. The column carries the number per watch so the default can
// move without a migration.
const DefaultWindowDays = 30

// DefaultMinMove is the smallest movement a percent watch reports by default,
// in dollars.
//
// It is the largest flat floor that leaves a watch on a $2.50-or-dearer card
// an exact percent watch, and it was chosen by measurement rather than taste.
// Simulating a trailing 10% drop across the owner's 1,959 held series over 103
// days: no floor fires 1,706 alerts (116 a week), of which 1,037 are on cards
// worth under a dollar, where ten percent is a nickel. A 25 cent floor removes
// 994 of those 1,037 and costs nothing at all above $2.50 — the $5-20 and
// $20-100 bands fire exactly 149 and 15 alerts either way.
//
// The larger flat floors buy quiet by breaking the feature instead: a dollar
// turns a 10% watch on a $3 card into a 33% watch, and five dollars into a
// 167% one, silently, so the card the user deliberately chose to watch is the
// card that stops answering. A price-proportional floor was measured and is
// degenerate — max(pct, k) is just a percent, and 0.25 x anchor fired 304
// alerts against 293 for a plain 25% watch with no floor, at every price
// alike, so it cannot tell a $3 card from a $136 one, which is the one thing
// it was proposed to do.
const DefaultMinMove = 0.25

// ParsePercent reads a movement's size, written as a percentage, into the
// fraction a watch stores.
//
// It lives here rather than on either input surface because it is the units
// boundary: the flag and the import file both spell a movement in percent, the
// store keeps a fraction, and one conversion decided once is what stops a
// factor of a hundred from appearing between them. Callers wrap the error in
// their own vocabulary — a flag name, or a file's line number.
//
// A bare number is a percentage and never a fraction: 10 is ten percent, not a
// thousand. That leaves one slip worth catching outright — the user who writes
// 0.10 meaning ten percent — because a 0.1% watch is a hair-trigger that fires
// on rounding, which reads as the feature being broken rather than as the
// number being misread. A drop of a hundred percent or more is refused too: it
// asks about a card becoming free.
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

// validateWatch refuses a watch whose op, finish, or units do not agree.
//
// The units check is the one worth having: threshold and pct are separate
// columns precisely so neither can quietly stand in for the other, and a row
// that fills the wrong one would evaluate against zero and either never fire
// or fire on every check.
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

// AddWatch records a dollar threshold, replacing any existing watch on the
// same card, finish and direction — re-adding is how a threshold is adjusted,
// and two alerts for one question would fire twice.
//
// The signature is the absolute watch's, unchanged, because the browser edits
// thresholds through it. A movement goes through AddWatchInput.
func (s *Store) AddWatch(scryfallID, display, finish, op string, threshold float64) error {
	return s.AddWatchInput(WatchInput{ScryfallID: scryfallID, Display: display,
		Finish: finish, Op: op, Threshold: threshold})
}

// AddWatchInput stands one fully specified watch, absolute or percent.
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

// WatchInput is one watch a bulk import stands.
type WatchInput struct {
	ScryfallID string
	Display    string
	Finish     string // nonfoil|foil|etched
	Op         string // under|over|drop|rise
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
}

// normalize fills the window a caller left unset. An absolute watch never
// reads the column, but every row carrying the same default is what keeps a
// later `UPDATE watches SET op='drop'` from inheriting a zero-day window.
func (w *WatchInput) normalize() {
	if w.WindowDays <= 0 {
		w.WindowDays = DefaultWindowDays
	}
}

// AddWatches stands every watch in one transaction — an interrupted import
// is nothing rather than half — and reports how many were new versus
// adjustments to a watch already standing. Duplicate inputs on one card,
// finish and direction upsert in order: the last row wins.
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

// effSourceExpr names the vendor behind a watch's effective price, for the one
// finish the watch is about. It mirrors effectivePrices' own CASE exactly: the
// Scryfall figure where there is one, else whichever vendor the alt table
// credits, and the empty string where nothing prices it.
//
// This is what a percent watch anchors on, and both halves of that matter.
//
// One vendor, because a movement is only checkable when both of its ends come
// from one series. Unrestricted, history threads between vendors as they
// answer: 1,897 consecutive observations in the owner's database switch source
// and 210 of those register as a ten percent move, so up to 6% of alerts would
// report which vendor answered rather than what the market did, with nothing
// in the output to tell the two apart.
//
// *This* vendor rather than a named one, because a fixed vendor is a fact
// about the past. This was a constant reading "tcgplayer" — 90.6% of recorded
// history, no flips by construction — and the measurement that retired it is
// worth keeping: hoard's effective price is now Scryfall's for 1,871 of 1,967
// printings, and comparing a tcgplayer anchor against it left 6.1% of held
// series already more than ten percent apart on the vendor gap alone, firing a
// phantom on the very first check and every check after. The feed had changed
// underneath the constant — tcgplayer went from 4,945 rows in one week to zero
// three weeks later — so the anchor was pinned to a series that had stopped
// being written. Following the effective price cannot go stale that way, and
// it also dissolves a staleness the fixed vendor created: 336 of 2,884 series
// had no tcgplayer observation in the last thirty days against 1 of 2,898
// across all sources.
const effSourceExpr = `
    CASE WHEN w.finish = 'etched' THEN
              CASE WHEN c.price_usd_etched IS NOT NULL
                     OR c.price_usd_foil   IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd_foil, '') END
         WHEN w.finish = 'foil' THEN
              CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd_foil, '') END
         ELSE CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                   ELSE COALESCE(a.source_usd, '') END END`

// anchorSeries is the observations a percent watch may read: one printing, one
// finish, one vendor.
//
// The finish clause is not decoration. A foil watch anchored on the nonfoil
// series would compare Ancient Tomb's $136 foil against a nonfoil high and
// report every foil watch as already met.
const anchorSeries = `
        FROM card_price_history h
       WHERE h.scryfall_id = ws.scryfall_id
         AND h.finish      = ws.finish
         AND h.source      = ws.eff_source`

// anchorFrom is the earliest observation the anchor may read.
//
// It opens at the last observation at or before the window's lower bound
// rather than at the bound itself, and that carry-forward is load-bearing.
// card_price_history is a change log — RecordPrices writes only when the price
// differs — so a printing whose price has not moved writes nothing at all, and
// a window can be empty of a series that is perfectly well known. Measured on
// the owner's database, 262 of 1,959 held priced series (13.4%) have no
// observation inside the last thirty days, Talon Gates of Madara among them:
// its price has been $132.14 since 12 May. Anchoring only inside the window
// would give those watches no anchor, and worse, the row that finally recorded
// a real drop would be the only row in the window, so the anchor would equal
// the fallen price and the alert would be lost rather than merely delayed.
//
// The bound the carry-forward opens from is the latest of three: the window,
// the watch's own creation — a watch cannot measure a move that predates it —
// and the moment it last fired, which is what re-anchors a trailing watch
// without any anchor being stored. last_fired_at and a never-created watch
// both default to the empty string, which sorts below every timestamp, so the
// tightest bound wins by construction and no case needs special handling.
const anchorFrom = `
    COALESCE((SELECT MAX(h.as_of) ` + anchorSeries + `
         AND h.as_of <= ws.anchor_lower), ws.anchor_lower)`

// watchBounds computes each watch's window in SQL from one bound "now", so the
// caller controls what the present is and every row still gets the window it
// asked for.
//
// The format string is the point. as_of is RFC 3339 (2026-08-10T21:14:12Z) and
// datetime() returns a space-separated form with no Z; SQLite compares them as
// text, and 'T' (0x54) sorts above ' ' (0x20), so a datetime() bound quietly
// admits the whole of the cutoff day. Measured on the owner's database: 24,345
// rows admitted where 23,684 is right. It is one day of slop that would
// survive any casual test, because in the common case the month digits differ
// first.
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

// watchStatusQuery reads every watch with its card, the price its question is
// about, and — for a movement — the extreme that movement is measured from, in
// a stable order.
//
// The anchor is derived on every read rather than stored. Because history
// records every distinct price transition, MAX over a date range *is* the
// running high, exactly, with no state to maintain: a stored anchor would need
// a write path in update-prices, would be frozen across any gap, and could
// drift with nothing in the row to say so.
const watchStatusQuery = watchBounds + `,
ws AS (
    SELECT w.*,
           c.name AS card_name, c.set_code, c.collector_number,
           COALESCE(c.mtgjson_uuid, '') AS uuid, c.promo_types,
           COALESCE(c.lang, '') AS card_lang,
           CASE WHEN w.finish = 'etched' THEN ` + effPriceEtched + `
                WHEN w.finish = 'foil'   THEN ` + effPriceFoil + `
                ELSE ` + effPriceUSD + ` END AS price,
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
       ws.promo_types, ws.card_lang, ws.price,
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

// ListWatches returns every watch with its current price, and every percent
// watch with the anchor it measures against as of now.
func (s *Store) ListWatches() ([]WatchStatus, error) { return s.listWatchesAt(now()) }

// listWatchesAt is ListWatches with the present named, so a test can put a
// window somewhere other than around the moment it runs.
func (s *Store) listWatchesAt(at string) ([]WatchStatus, error) {
	rows, err := s.db.Query(watchStatusQuery, at)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	defer rows.Close()
	var out []WatchStatus
	for rows.Next() {
		var w WatchStatus
		var promos sql.NullString
		if err := rows.Scan(&w.ID, &w.ScryfallID, &w.Display, &w.Finish, &w.Op,
			&w.Threshold, &w.Pct, &w.MinMove, &w.WindowDays, &w.CreatedAt,
			&w.LastState, &w.LastFiredAt,
			&w.Name, &w.SetCode, &w.CollectorNumber, &w.MTGJSONUUID,
			&promos, &w.Lang, &w.PriceUSD,
			&w.Anchor, &w.AnchorAt, &w.HistorySince, &w.WindowFrom); err != nil {
			return nil, err
		}
		w.Treatment = FoilTreatment(promos)
		out = append(out, w)
	}
	return out, rows.Err()
}

// CheckWatches evaluates every watch against stored prices — no network —
// and returns the ones that just crossed into their condition, alongside how
// many were checked at all.
//
// A watch fires when its condition holds now and did not at the previous
// check; that includes the first check ever, because "already under your
// threshold" is worth exactly one alert. An unpriced card is skipped with
// its state untouched: no price answers neither "under" nor "over", and
// flipping to unmet would re-fire the alert when the price comes back.
func (s *Store) CheckWatches() (fired []WatchStatus, checked int, err error) {
	return s.checkWatchesAt(now())
}

// checkWatchesAt is CheckWatches with the present named, so a test can walk a
// price series past a window boundary instead of waiting for one.
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
	// Firing re-anchors a percent watch, and that is the notification design
	// rather than a detail of it. Crossing semantics alone are not enough for a
	// trailing anchor: measured on Prismatic Vista, a single continuous slide
	// fired on 11 July, un-met on the 12th when the price bounced back over the
	// line, and fired again on the 13th — two alerts for one slide, because the
	// anchor did not move when the alert did. Writing the fire moment makes it
	// the anchor's lower bound, so the anchor collapses to about the price that
	// fired and the condition immediately reads unmet. The watch is re-armed at
	// the new level and will speak again only on a *further* move of its own
	// size, which is a second alert worth having.
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

// WatchByRef resolves a watch by numeric id or a case-insensitive fragment
// of its display name, refusing an ambiguous fragment — the same contract as
// binder and deck refs.
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

// HasAnchorSeries reports whether the printing has any observation in the
// series a percent watch would anchor on — the one its effective price comes
// from. A movement cannot be measured against a series that does not exist,
// and saying so when the watch is set is the difference between a refusal and
// an alert that never arrives.
//
// It reads false while the vendor answering for a printing has changed and its
// price has not moved since, because history records transitions and a
// changeover at an unchanged price writes nothing. That resolves itself at the
// first real move, and until then the watch shows as waiting rather than
// pretending to an anchor.
func (s *Store) HasAnchorSeries(scryfallID, finish string) (bool, error) {
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
		scryfallID, finish).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("checking price history: %w", err)
	}
	return ok, nil
}

// RemoveWatch deletes one watch by id.
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

// WouldFire returns the watches whose condition holds now but did not at
// the last recorded check — exactly what CheckWatches would report —
// without writing any state. The browser's banner previews alerts this
// way: a glance at the TUI is not an acknowledgment, and consuming the
// crossing here would silently eat the alert a cron report depends on.
//
// last_fired_at falls under the same doctrine and is likewise not written.
// Writing it would re-anchor a percent watch on a glance, which does not just
// eat the alert — it moves the baseline the next one is measured from, so the
// movement a cron would have reported is gone rather than merely delayed.
func (s *Store) WouldFire() ([]WatchStatus, error) { return s.wouldFireAt(now()) }

func (s *Store) wouldFireAt(ts string) ([]WatchStatus, error) {
	all, err := s.listWatchesAt(ts)
	if err != nil {
		return nil, err
	}
	var fired []WatchStatus
	for _, w := range all {
		if w.PriceUSD != nil && w.Met() && w.LastState != "met" {
			fired = append(fired, w)
		}
	}
	return fired, nil
}
