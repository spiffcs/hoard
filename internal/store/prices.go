package store

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// SourceCount is how much of the collection one price source covers: a
// printing-finish combination counts once however many copies are held, and
// Copies counts them all. Source is "scryfall", a fallback vendor's name, or
// "" for the printings nothing can price.
type SourceCount struct {
	Source    string
	Printings int
	Copies    int
}

// PriceSources reports where every owned printing-finish's price comes from,
// largest source first. The valuation report prints this so a reader knows
// which figures are Scryfall's, which are a fallback vendor's estimate, and
// how much of the total is standing on nothing at all.
func (s *Store) PriceSources() ([]SourceCount, error) {
	// The inner query classifies each owned printing-finish exactly as
	// entryValue prices it: the foil price for foil and etched, with Scryfall
	// preferred over the fallback — so the counts and the totals they explain
	// cannot disagree.
	rows, err := s.db.Query(`
SELECT src, COUNT(*) AS printings, SUM(copies) AS copies FROM (
  SELECT CASE WHEN e.finish IN ('foil','etched') THEN
           CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                WHEN a.price_usd_foil IS NOT NULL THEN a.source_usd_foil
                ELSE '' END
         ELSE
           CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
                WHEN a.price_usd IS NOT NULL THEN a.source_usd
                ELSE '' END
         END AS src,
         SUM(e.quantity) AS copies
  FROM card_entries e
  JOIN cards c ON c.scryfall_id = e.scryfall_id
  ` + altJoinEntries + `
  GROUP BY c.scryfall_id, e.finish
)
GROUP BY src
ORDER BY printings DESC, src`)
	if err != nil {
		return nil, fmt.Errorf("counting price sources: %w", err)
	}
	defer rows.Close()
	var out []SourceCount
	for rows.Next() {
		var sc SourceCount
		if err := rows.Scan(&sc.Source, &sc.Printings, &sc.Copies); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// LatestPriceStamp is when the prices being reported were observed: the
// newest history row, or — before any history exists — the newest card
// fetch. Empty only when the database has never seen a price at all. A
// valuation carries this date because "what it's worth" is meaningless
// without "as of when".
func (s *Store) LatestPriceStamp() (string, error) {
	var stamp sql.NullString
	if err := s.db.QueryRow(
		`SELECT MAX(as_of) FROM card_price_history`).Scan(&stamp); err != nil {
		return "", fmt.Errorf("reading latest observation: %w", err)
	}
	if stamp.String != "" {
		return stamp.String, nil
	}
	if err := s.db.QueryRow(
		`SELECT MAX(updated_at) FROM cards WHERE price_usd IS NOT NULL
		    OR price_usd_foil IS NOT NULL`).Scan(&stamp); err != nil {
		return "", fmt.Errorf("reading latest fetch: %w", err)
	}
	return stamp.String, nil
}

// Prices Scryfall could not supply, and the gaps that remain after trying.

// PriceGap is a card that has no price for a finish it is actually owned in.
type PriceGap struct {
	ScryfallID string
	SetCode    string
	Name       string
	// CheckedAt is when MTGJSON was last asked about this card and had nothing,
	// or nil if it has never been asked. Callers use it to avoid paying for a
	// 50 MB scan to re-learn an answer they already have.
	CheckedAt *string
}

// UnpricedByOwnedFinish returns cards Scryfall could not price for a finish the
// user actually holds.
//
// The finish matters: a card owned only in non-foil needs no foil price, and
// counting it as a gap would send every run chasing prices that will never be
// used. Cards already carrying a usable fallback are excluded too, so a second
// run is cheap.
func (s *Store) UnpricedByOwnedFinish() ([]PriceGap, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT c.scryfall_id, c.set_code, c.name, g.checked_at
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
LEFT JOIN card_price_gaps g ON g.scryfall_id = c.scryfall_id
` + altJoinCards + `
WHERE ` + unpricedPredicate + `
ORDER BY c.set_code, c.name`)
	if err != nil {
		return nil, fmt.Errorf("finding unpriced cards: %w", err)
	}
	defer rows.Close()
	var out []PriceGap
	for rows.Next() {
		var g PriceGap
		if err := rows.Scan(&g.ScryfallID, &g.SetCode, &g.Name, &g.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UnpricedRow is one card-and-finish that no source can price, with where it is
// held so the reader can see which totals are understated.
type UnpricedRow struct {
	ScryfallID      string
	MTGJSONUUID     string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Copies          int
	// Containers holds each container's display label, sorted; HeldIn is the
	// comma-joined form for one-cell display. Both exist because a name may
	// itself contain a comma, which makes the joined form unsplittable.
	Containers []string
	HeldIn     string
	// ColorIdentity is the printing's WUBRG identity, nil when unknown —
	// same semantics as Card.ColorIdentity.
	ColorIdentity []string
}

// Unpriced lists the same gaps as UnpricedByOwnedFinish, broken out per finish
// and annotated for display.
//
// Kept separate from UnpricedByOwnedFinish because the two want different
// shapes: the fill needs distinct cards to look up, this needs one row per
// finish with its containers. They share unpricedPredicate so the two can never
// disagree about what counts as unpriced.
func (s *Store) Unpriced() ([]UnpricedRow, error) {
	// The labels are aggregated twice: once with DISTINCT for the display
	// string, and once more on an unprintable separator (unit separator, 0x1f)
	// for the list — a name may contain a comma, so the display string cannot
	// be split back apart, and SQLite refuses a custom separator alongside
	// DISTINCT, so one aggregate cannot serve both.
	rows, err := s.db.Query(`
SELECT c.scryfall_id, COALESCE(c.mtgjson_uuid, ''),
       c.name, c.set_code, c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       GROUP_CONCAT(DISTINCT ` + containerLabel + `) AS held_in,
       GROUP_CONCAT(` + containerLabel + `, char(31)) AS held_in_raw,
       c.color_identity
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
` + altJoinCards + `
WHERE ` + unpricedPredicate + `
GROUP BY c.scryfall_id, e.finish
ORDER BY c.name, e.finish`)
	if err != nil {
		return nil, fmt.Errorf("listing unpriced cards: %w", err)
	}
	defer rows.Close()
	var out []UnpricedRow
	for rows.Next() {
		var u UnpricedRow
		var raw string
		var colors sql.NullString
		if err := rows.Scan(&u.ScryfallID, &u.MTGJSONUUID, &u.Name, &u.SetCode,
			&u.CollectorNumber, &u.Finish, &u.Copies, &u.HeldIn, &raw, &colors); err != nil {
			return nil, err
		}
		u.ColorIdentity = parseColorIdentity(colors)
		u.Containers = dedupeSorted(strings.Split(raw, "\x1f"))
		out = append(out, u)
	}
	return out, rows.Err()
}

// dedupeSorted sorts labels and drops duplicates — the Go half of a DISTINCT
// the SQL aggregate above is not allowed to express.
func dedupeSorted(labels []string) []string {
	slices.Sort(labels)
	return slices.Compact(labels)
}

// AltPrice is a fallback price to record for one card.
//
// The two finishes carry their own vendor because they are looked up
// independently: the shop that prices a card's non-foil printing often has no
// figure for its foil, and vice versa.
type AltPrice struct {
	ScryfallID    string
	MTGJSONUUID   string
	PriceUSD      *float64
	PriceUSDFoil  *float64
	SourceUSD     string
	SourceUSDFoil string
}

// UpsertAltPrices records fallback prices, replacing any previous ones.
//
// Rows are rewritten rather than inserted once: a vendor's price moves, and a
// card Scryfall later learns to price should not be left showing a stale
// fallback underneath it.
func (s *Store) UpsertAltPrices(prices []AltPrice) error {
	if len(prices) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_prices_alt (scryfall_id, mtgjson_uuid, price_usd, price_usd_foil,
                             source_usd, source_usd_foil, as_of)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    mtgjson_uuid    = excluded.mtgjson_uuid,
    price_usd       = excluded.price_usd,
    price_usd_foil  = excluded.price_usd_foil,
    source_usd      = excluded.source_usd,
    source_usd_foil = excluded.source_usd_foil,
    as_of           = excluded.as_of`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, p := range prices {
		if _, err := stmt.Exec(p.ScryfallID, p.MTGJSONUUID, p.PriceUSD,
			p.PriceUSDFoil, nullable(p.SourceUSD), nullable(p.SourceUSDFoil), ts); err != nil {
			return fmt.Errorf("recording fallback price for %s: %w", p.ScryfallID, err)
		}
	}
	return tx.Commit()
}

// RecordPriceGapChecks notes that MTGJSON was asked about these cards and had
// no usable price.
//
// Recorded for every card asked about, not only the ones that came back empty:
// a card MTGJSON priced is no longer a gap, so it will not be asked about again
// regardless, and writing a row for it would be describing a state that no
// longer exists.
func (s *Store) RecordPriceGapChecks(scryfallIDs []string) error {
	if len(scryfallIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_price_gaps (scryfall_id, checked_at) VALUES (?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET checked_at = excluded.checked_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, id := range scryfallIDs {
		if _, err := stmt.Exec(id, ts); err != nil {
			return fmt.Errorf("recording price-gap check for %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// nullable stores an empty string as SQL NULL, so "no vendor for this finish"
// is distinguishable from a vendor whose name happens to be blank.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
