package store

import "fmt"

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
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Copies          int
	HeldIn          string
}

// Unpriced lists the same gaps as UnpricedByOwnedFinish, broken out per finish
// and annotated for display.
//
// Kept separate from UnpricedByOwnedFinish because the two want different
// shapes: the fill needs distinct cards to look up, this needs one row per
// finish with its containers. They share unpricedPredicate so the two can never
// disagree about what counts as unpriced.
func (s *Store) Unpriced() ([]UnpricedRow, error) {
	rows, err := s.db.Query(`
SELECT c.name, c.set_code, c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       GROUP_CONCAT(DISTINCT ` + containerLabel + `) AS held_in
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
		if err := rows.Scan(&u.Name, &u.SetCode, &u.CollectorNumber,
			&u.Finish, &u.Copies, &u.HeldIn); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
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
