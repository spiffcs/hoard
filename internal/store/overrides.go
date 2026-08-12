package store

// Price corrections: the prices hoard refused, and what it used instead.
//
// See migration v29 for why this table is allowed to outrank the catalog, and
// internal/pricing/contradiction.go for the judgement that fills it. This file
// is only the storage: what the sweep needs to read, and how its answer lands.

import (
	"database/sql"
	"strings"
	"time"
)

// PriceCandidate is one owned printing-and-finish the contradiction sweep can
// check, with the price it would be reported at if no correction existed.
//
// Price is the *uncorrected* figure (see catalogPriceUSD) and is zero when
// nothing prices this finish at all — an unpriced card cannot be contradicted,
// but it is still carried here so a caller can tell "no price" from "not a
// candidate" without a second query.
type PriceCandidate struct {
	ScryfallID string
	SetCode    string
	Finish     string
	Price      float64

	// ProductID is the printing's TCGplayer product, and AltProductID and
	// EtchedProductID the separately-sold treated foil and etched products
	// when they exist. Which one prices this row's finish is the caller's
	// judgement — see pricing.productFor.
	ProductID       string
	AltProductID    string
	EtchedProductID string
}

// PriceCandidates lists every owned printing-and-finish that TCGplayer sells a
// product for, so the contradiction sweep has something to compare against.
//
// Held finishes only. A foil price on a card owned only in non-foil is not
// worth a network round trip and could never reach a total, which is the same
// rule unpricedPredicate applies for the same reason.
func (s *Store) PriceCandidates() ([]PriceCandidate, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT c.scryfall_id, c.set_code, e.finish,
       COALESCE(CASE
           WHEN e.finish = 'etched' THEN ` + catalogPriceEtched + `
           WHEN e.finish = 'foil'   THEN ` + catalogPriceFoil + `
           ELSE ` + catalogPriceUSD + ` END, 0),
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

// PriceOverride is one printing's corrections, one finish at a time — the
// shape the sweep produces and the worklist reads.
type PriceOverride struct {
	ScryfallID string
	Finish     string
	// Price is the substitute and Refused the figure it replaced.
	Price, Refused float64
	// Source names where the substitute came from and Reason why the refusal
	// happened, both stored verbatim for display.
	Source, Reason string
	AsOf           string
}

// ReplacePriceOverrides makes the corrections for the printings in checked say
// exactly this and nothing else, in one transaction.
//
// Replacement rather than an upsert, because a correction that no longer
// applies has to disappear. The refused figures are transient by nature:
// TCGplayer's market price for a preorder becomes real the day the card starts
// selling, and a correction that outlived its evidence would go on quietly
// restating a stale ask as the card's value.
//
// checked is what bounds that deletion, and passing the wrong thing here is
// the dangerous mistake this signature exists to prevent. It must be the
// printings whose asks were genuinely re-read this pass — not every printing
// owned. A sweep cut short by an unreachable catalog produces no corrections,
// and deleting on that basis would restore the refused price as the card's
// value and look like a repair. An empty checked therefore changes nothing at
// all, which is the correct answer to "nothing was examined".
func (s *Store) ReplacePriceOverrides(overrides []PriceOverride, checked []string) error {
	if len(checked) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Chunked because checked is the whole owned collection on an ordinary
	// run, well past what one statement should carry as bind parameters.
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

	// One row per printing with a column per finish, so the corrections are
	// folded here rather than written a row at a time.
	type row struct {
		price, refused map[string]float64
		source, reason string
	}
	byCard := map[string]*row{}
	var order []string
	for _, o := range overrides {
		r, ok := byCard[o.ScryfallID]
		if !ok {
			r = &row{price: map[string]float64{}, refused: map[string]float64{}}
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
	null := func(m map[string]float64, finish string) any {
		if v, ok := m[finish]; ok {
			return v
		}
		return nil
	}
	for _, id := range order {
		r := byCard[id]
		if _, err := stmt.Exec(id,
			null(r.price, "nonfoil"), null(r.price, "foil"), null(r.price, "etched"),
			null(r.refused, "nonfoil"), null(r.refused, "foil"), null(r.refused, "etched"),
			r.source, r.reason, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PriceOverrideRow is one correction with enough of the card beside it to be
// listed without a second lookup.
type PriceOverrideRow struct {
	PriceOverride
	Name            string
	SetCode         string
	CollectorNumber string
	Quantity        int
}

// PriceOverrides lists every correction in force, newest cards first by name,
// for the worklist. Unpivoted back to one row per finish: the table stores a
// column per finish to keep the valuation join cheap, but a reader wants the
// corrections one at a time.
func (s *Store) PriceOverrides() ([]PriceOverrideRow, error) {
	// Unpivoted with UNION ALL, the same shape effectivePrices uses: SQLite has
	// no LATERAL, and three explicit branches read better than a CASE per
	// column anyway.
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
