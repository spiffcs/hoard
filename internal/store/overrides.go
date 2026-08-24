package store

import (
	"database/sql"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
)

type PriceCandidate struct {
	ScryfallID string
	SetCode    string
	Finish     finish.Finish
	Price      float64

	ProductID       string
	AltProductID    string
	EtchedProductID string
}

func (s *Store) PriceCandidates() ([]PriceCandidate, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT c.scryfall_id, c.set_code, e.finish,
       COALESCE(CASE
           WHEN e.finish = 'etched' THEN ` + catalogPriceEtched + `
           WHEN e.finish = 'foil'    THEN ` + catalogPriceFoil + `
           WHEN e.finish = 'nonfoil' THEN ` + catalogPriceUSD + ` END, 0),
       COALESCE(c.tcg_product_id, ''),
       COALESCE(c.tcg_alt_product_id, ''),
       COALESCE(c.tcg_etched_product_id, '')
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
LEFT JOIN card_prices_alt a ON a.scryfall_id = e.scryfall_id
WHERE COALESCE(c.tcg_product_id, '') <> ''
   OR COALESCE(c.tcg_alt_product_id, '') <> ''
   OR COALESCE(c.tcg_etched_product_id, '') <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceCandidate
	for rows.Next() {
		var c PriceCandidate
		if err := rows.Scan(&c.ScryfallID, &c.SetCode, &c.Finish, &c.Price,
			&c.ProductID, &c.AltProductID, &c.EtchedProductID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type PriceOverride struct {
	ScryfallID string
	Finish     finish.Finish

	Price, Refused float64

	Source, Reason string
	AsOf           string
}

func (s *Store) ReplacePriceOverrides(overrides []PriceOverride, checked []string) error {
	if len(checked) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const chunk = 400
	for start := 0; start < len(checked); start += chunk {
		batch := checked[start:min(start+chunk, len(checked))]
		q := `DELETE FROM card_price_overrides WHERE scryfall_id IN (?` +
			strings.Repeat(",?", len(batch)-1) + `)`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	if len(overrides) == 0 {
		return tx.Commit()
	}

	type row struct {
		price, refused map[finish.Finish]float64
		source, reason string
	}
	byCard := map[string]*row{}
	var order []string
	for _, o := range overrides {
		r, ok := byCard[o.ScryfallID]
		if !ok {
			r = &row{price: map[finish.Finish]float64{}, refused: map[finish.Finish]float64{}}
			byCard[o.ScryfallID], order = r, append(order, o.ScryfallID)
		}
		r.price[o.Finish], r.refused[o.Finish] = o.Price, o.Refused
		r.source, r.reason = o.Source, o.Reason
	}

	stmt, err := tx.Prepare(`
INSERT INTO card_price_overrides (scryfall_id,
    price_usd, price_usd_foil, price_usd_etched,
    refused_usd, refused_usd_foil, refused_usd_etched,
    source, reason, as_of)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	null := func(m map[finish.Finish]float64, f finish.Finish) any {
		if v, ok := m[f]; ok {
			return v
		}
		return nil
	}
	for _, id := range order {
		r := byCard[id]
		if _, err := stmt.Exec(id,
			null(r.price, finish.Nonfoil), null(r.price, finish.Foil), null(r.price, finish.Etched),
			null(r.refused, finish.Nonfoil), null(r.refused, finish.Foil), null(r.refused, finish.Etched),
			r.source, r.reason, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type PriceOverrideRow struct {
	PriceOverride
	Name            string
	SetCode         string
	CollectorNumber string
	Quantity        int
}

func (s *Store) PriceOverrides() ([]PriceOverrideRow, error) {

	rows, err := s.db.Query(`
WITH corrections AS (
    SELECT scryfall_id, 'nonfoil' AS finish, price_usd AS price,
           refused_usd AS refused, source, reason, as_of
      FROM card_price_overrides WHERE price_usd IS NOT NULL
    UNION ALL
    SELECT scryfall_id, 'foil', price_usd_foil, refused_usd_foil, source, reason, as_of
      FROM card_price_overrides WHERE price_usd_foil IS NOT NULL
    UNION ALL
    SELECT scryfall_id, 'etched', price_usd_etched, refused_usd_etched, source, reason, as_of
      FROM card_price_overrides WHERE price_usd_etched IS NOT NULL
)
SELECT o.scryfall_id, o.finish, o.price, o.refused, o.source, o.reason, o.as_of,
       c.name, c.set_code, c.collector_number,
       COALESCE((SELECT SUM(e.quantity) FROM card_entries e
                  WHERE e.scryfall_id = o.scryfall_id AND e.finish = o.finish), 0)
FROM corrections o
JOIN cards c ON c.scryfall_id = o.scryfall_id
ORDER BY c.name, o.finish`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceOverrideRow
	for rows.Next() {
		var r PriceOverrideRow
		var refused sql.NullFloat64
		if err := rows.Scan(&r.ScryfallID, &r.Finish, &r.Price, &refused,
			&r.Source, &r.Reason, &r.AsOf,
			&r.Name, &r.SetCode, &r.CollectorNumber, &r.Quantity); err != nil {
			return nil, err
		}
		r.Refused = refused.Float64
		out = append(out, r)
	}
	return out, rows.Err()
}
