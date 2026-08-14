package store

import (
	"database/sql"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"strings"
)

type SourceCount struct {
	Source    string
	Printings int
	Copies    int
}

func (s *Store) PriceSources() ([]SourceCount, error) {

	rows, err := s.db.Query(`
SELECT src, COUNT(*) AS printings, SUM(copies) AS copies FROM (
  SELECT CASE
         WHEN e.finish = 'etched' THEN
           CASE WHEN c.price_usd_etched IS NOT NULL THEN 'scryfall'
                WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                WHEN a.price_usd_foil IS NOT NULL THEN a.source_usd_foil
                ELSE '' END
         WHEN e.finish = 'foil' THEN
           CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
                WHEN a.price_usd_foil IS NOT NULL THEN a.source_usd_foil
                ELSE '' END
         WHEN e.finish = 'nonfoil' THEN
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

type PriceGap struct {
	ScryfallID string
	SetCode    string
	Name       string

	CheckedAt *string
}

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

func (s *Store) AltPricedOwned() ([]PriceGap, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT c.scryfall_id, c.set_code, c.name, NULL
FROM card_prices_alt a
JOIN cards c ON c.scryfall_id = a.scryfall_id
JOIN card_entries e ON e.scryfall_id = c.scryfall_id
ORDER BY c.set_code, c.name`)
	if err != nil {
		return nil, fmt.Errorf("finding fallback-priced cards: %w", err)
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

type UnpricedRow struct {
	ScryfallID      string
	MTGJSONUUID     string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          finish.Finish
	Copies          int

	Containers []string
	HeldIn     string

	Treatment string

	ColorIdentity []string

	Lang string
}

func (s *Store) Unpriced() ([]UnpricedRow, error) {

	rows, err := s.db.Query(`
SELECT c.scryfall_id, COALESCE(c.mtgjson_uuid, ''),
       c.name, c.set_code, c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       GROUP_CONCAT(DISTINCT ct.name) AS held_in,
       GROUP_CONCAT(ct.name, char(31)) AS held_in_raw,
       c.color_identity, c.promo_types, COALESCE(c.lang, '')
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
		var colors, promos sql.NullString
		if err := rows.Scan(&u.ScryfallID, &u.MTGJSONUUID, &u.Name, &u.SetCode,
			&u.CollectorNumber, &u.Finish, &u.Copies, &u.HeldIn, &raw, &colors, &promos,
			&u.Lang); err != nil {
			return nil, err
		}
		u.ColorIdentity = parseColorIdentity(colors)
		u.Treatment = FoilTreatment(promos)
		u.Containers = dedupeSorted(strings.Split(raw, "\x1f"))
		out = append(out, u)
	}
	return out, rows.Err()
}

func dedupeSorted(labels []string) []string {
	slices.Sort(labels)
	return slices.Compact(labels)
}

type AltPrice struct {
	ScryfallID    string
	MTGJSONUUID   string
	PriceUSD      *float64
	PriceUSDFoil  *float64
	SourceUSD     string
	SourceUSDFoil string
}

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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
