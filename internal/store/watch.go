package store

// Price watches: standing thresholds on individual printings, checked
// against stored prices. The intended pairing is a cron's
// `hoard update-prices && hoard watch` — the first fetches reality, the
// second answers "did anything I care about cross a line?".

import (
	"fmt"
	"strconv"
	"strings"
)

// Watch is one standing threshold. Op is "under" or "over", strict in both
// directions: under 30 means the price fell below $30.00, not to it.
// LastState remembers the previous check ("met", "unmet", or "" before the
// first one), which is what makes an alert fire on the crossing rather than
// on every check it sits past the threshold.
type Watch struct {
	ID         int64
	ScryfallID string
	// Display is the card's name as resolved at creation; the set, number
	// and price are joined from the catalog at read time.
	Display   string
	Finish    string // nonfoil|foil — the two finishes prices come in
	Op        string // under|over
	Threshold float64
	CreatedAt string
	LastState string
}

// WatchStatus is a watch beside what the catalog currently knows about its
// card. Met is meaningful only when PriceUSD is non-nil: an unpriced card
// answers neither "under" nor "over".
type WatchStatus struct {
	Watch
	Name            string
	SetCode         string
	CollectorNumber string
	MTGJSONUUID     string
	PriceUSD        *float64
}

// Met reports whether the watch's condition currently holds.
func (w WatchStatus) Met() bool {
	if w.PriceUSD == nil {
		return false
	}
	if w.Op == "under" {
		return *w.PriceUSD < w.Threshold
	}
	return *w.PriceUSD > w.Threshold
}

// AddWatch records a threshold, replacing any existing watch on the same
// card, finish and direction — re-adding is how a threshold is adjusted, and
// two alerts for one question would fire twice. Replacement resets
// LastState: the new threshold has not been checked yet.
func (s *Store) AddWatch(scryfallID, display, finish, op string, threshold float64) error {
	if op != "under" && op != "over" {
		return fmt.Errorf("watch op must be under or over, not %q", op)
	}
	if finish != "nonfoil" && finish != "foil" {
		return fmt.Errorf("watch finish must be nonfoil or foil, not %q", finish)
	}
	_, err := s.db.Exec(`
INSERT INTO watches (scryfall_id, display, finish, op, threshold, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id, finish, op) DO UPDATE SET
    threshold  = excluded.threshold,
    display    = excluded.display,
    last_state = ''`,
		scryfallID, display, finish, op, threshold, now())
	if err != nil {
		return fmt.Errorf("recording watch: %w", err)
	}
	return nil
}

// watchStatusQuery reads every watch with its card and current effective
// price, in a stable order.
const watchStatusQuery = `
SELECT w.id, w.scryfall_id, w.display, w.finish, w.op, w.threshold,
       w.created_at, w.last_state,
       c.name, c.set_code, c.collector_number, COALESCE(c.mtgjson_uuid, ''),
       CASE WHEN w.finish = 'foil' THEN ` + effPriceFoil + ` ELSE ` + effPriceUSD + ` END
FROM watches w
JOIN cards c ON c.scryfall_id = w.scryfall_id
` + altJoinCards + `
ORDER BY w.display, w.finish, w.op, w.id`

// ListWatches returns every watch with its current price.
func (s *Store) ListWatches() ([]WatchStatus, error) {
	rows, err := s.db.Query(watchStatusQuery)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	defer rows.Close()
	var out []WatchStatus
	for rows.Next() {
		var w WatchStatus
		if err := rows.Scan(&w.ID, &w.ScryfallID, &w.Display, &w.Finish, &w.Op,
			&w.Threshold, &w.CreatedAt, &w.LastState,
			&w.Name, &w.SetCode, &w.CollectorNumber, &w.MTGJSONUUID,
			&w.PriceUSD); err != nil {
			return nil, err
		}
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
	all, err := s.ListWatches()
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
			names[i] = fmt.Sprintf("%d: %s %s %s $%.2f", w.ID, w.Display, w.Finish, w.Op, w.Threshold)
		}
		return WatchStatus{}, fmt.Errorf("%q matches %d watches:\n  %s\nuse the id",
			ref, len(matches), strings.Join(names, "\n  "))
	}
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
