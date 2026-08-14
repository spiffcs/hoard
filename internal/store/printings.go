package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func (s *Store) UpsertPrintings(cards []scryfall.Card) error {
	if len(cards) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertPrintingsTx(tx, cards); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertPrintingsTx(tx *sql.Tx, cards []scryfall.Card) error {

	stmt, err := tx.Prepare(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, price_usd_etched,
                   scryfall_url, updated_at, raw_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    set_code         = excluded.set_code,
    collector_number = excluded.collector_number,
    name             = excluded.name,
    price_usd        = excluded.price_usd,
    price_usd_foil   = excluded.price_usd_foil,
    price_usd_etched = excluded.price_usd_etched,
    scryfall_url     = excluded.scryfall_url,
    updated_at       = excluded.updated_at,
    raw_json         = COALESCE(excluded.raw_json, cards.raw_json)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, c := range cards {
		var raw any
		if len(c.Raw) > 0 {
			raw = string(c.Raw)
		}
		if _, err := stmt.Exec(c.ID, c.Set, c.CollectorNumber, c.Name,
			c.PriceUSD, c.PriceUSDFoil, c.PriceUSDEtched, c.ScryfallURL, ts, raw); err != nil {
			return fmt.Errorf("upserting catalog card %s: %w", c.Name, err)
		}
	}
	return nil
}

func mergePrintingsTx(tx *sql.Tx, ps []SourcePrinting) error {
	stmt, err := tx.Prepare(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, price_usd_etched,
                   scryfall_url, updated_at, raw_json, mtgjson_uuid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    set_code         = excluded.set_code,
    collector_number = excluded.collector_number,
    name             = excluded.name,
    price_usd        = excluded.price_usd,
    price_usd_foil   = excluded.price_usd_foil,
    price_usd_etched = excluded.price_usd_etched,
    scryfall_url     = excluded.scryfall_url,
    updated_at       = excluded.updated_at,
    raw_json         = COALESCE(excluded.raw_json, cards.raw_json),
    mtgjson_uuid     = COALESCE(excluded.mtgjson_uuid, cards.mtgjson_uuid)
WHERE excluded.updated_at >= cards.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	fill, err := tx.Prepare(`
UPDATE cards SET raw_json = COALESCE(raw_json, ?),
                 mtgjson_uuid = COALESCE(mtgjson_uuid, ?)
WHERE scryfall_id = ? AND (raw_json IS NULL OR mtgjson_uuid IS NULL)`)
	if err != nil {
		return err
	}
	defer fill.Close()

	for _, p := range ps {
		var raw, uuid any
		if len(p.Card.Raw) > 0 {
			raw = string(p.Card.Raw)
		}
		if p.MTGJSONUUID != "" {
			uuid = p.MTGJSONUUID
		}
		if _, err := stmt.Exec(p.Card.ID, p.Card.Set, p.Card.CollectorNumber,
			p.Card.Name, p.Card.PriceUSD, p.Card.PriceUSDFoil, p.Card.PriceUSDEtched,
			p.Card.ScryfallURL, p.UpdatedAt, raw, uuid); err != nil {
			return fmt.Errorf("merging printing %s: %w", p.Card.Name, err)
		}
		if raw == nil && uuid == nil {
			continue
		}
		if _, err := fill.Exec(raw, uuid, p.Card.ID); err != nil {
			return fmt.Errorf("merging printing %s: %w", p.Card.Name, err)
		}
	}
	return nil
}

func (s *Store) ActivePrintingIDs() ([]string, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id FROM cards c
WHERE EXISTS (SELECT 1 FROM card_entries e WHERE e.scryfall_id = c.scryfall_id)
   OR EXISTS (SELECT 1 FROM watches w WHERE w.scryfall_id = c.scryfall_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) IDsNeedingDocuments() ([]string, error) {
	rows, err := s.db.Query(`SELECT scryfall_id FROM cards WHERE raw_json IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("finding cards without a stored document: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) KnownMTGJSONUUIDs() (map[string]string, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, mtgjson_uuid FROM cards WHERE mtgjson_uuid IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sid, uuid string
		if err := rows.Scan(&sid, &uuid); err != nil {
			return nil, err
		}
		out[sid] = uuid
	}
	return out, rows.Err()
}

func (s *Store) SaveMTGJSONUUIDs(ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE cards SET mtgjson_uuid = ? WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, uuid := range ids {
		if uuid == "" {
			continue
		}
		if _, err := stmt.Exec(uuid, sid); err != nil {
			return fmt.Errorf("caching mtgjson id for %s: %w", sid, err)
		}
	}
	return tx.Commit()
}

type CKLinks struct {
	URL     string
	FoilURL string
}

func (s *Store) KnownCardKingdomLinks() (map[string]bool, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id FROM cards WHERE ck_url IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		out[sid] = true
	}
	return out, rows.Err()
}

func (s *Store) TCGAltProducts() (foil, etched map[string]string, stamped map[string]bool, err error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, tcg_alt_product_id, COALESCE(tcg_etched_product_id, '')
FROM cards WHERE tcg_alt_product_id IS NOT NULL`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	foil, etched, stamped = map[string]string{}, map[string]string{}, map[string]bool{}
	for rows.Next() {
		var sid, pid, eid string
		if err := rows.Scan(&sid, &pid, &eid); err != nil {
			return nil, nil, nil, err
		}
		stamped[sid] = true
		if pid != "" {
			foil[sid] = pid
		}
		if eid != "" {
			etched[sid] = eid
		}
	}
	return foil, etched, stamped, rows.Err()
}

func (s *Store) SaveTCGAltProducts(foil, etched map[string]string) error {
	if len(foil) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
UPDATE cards SET tcg_alt_product_id = ?, tcg_etched_product_id = ?
WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, pid := range foil {
		if _, err := stmt.Exec(pid, etched[sid], sid); err != nil {
			return fmt.Errorf("caching tcg product id for %s: %w", sid, err)
		}
	}
	return tx.Commit()
}

func (s *Store) KnownVendorProductIDs() (map[string]bool, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id FROM cards WHERE ck_foil_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		out[sid] = true
	}
	return out, rows.Err()
}

type VendorProductIDs struct {
	TCGProduct string
	CKFoil     string
	CKEtched   string
}

func (s *Store) SaveVendorProductIDs(ids map[string]VendorProductIDs) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
UPDATE cards SET tcg_product_id = ?, ck_foil_id = ?, ck_etched_id = ?
WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, v := range ids {
		if _, err := stmt.Exec(v.TCGProduct, v.CKFoil, v.CKEtched, sid); err != nil {
			return fmt.Errorf("caching vendor product ids for %s: %w", sid, err)
		}
	}
	return tx.Commit()
}

func (s *Store) SaveCardKingdomLinks(links map[string]CKLinks) error {
	if len(links) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE cards SET ck_url = ?, ck_foil_url = ? WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, l := range links {
		if _, err := stmt.Exec(l.URL, l.FoilURL, sid); err != nil {
			return fmt.Errorf("caching card kingdom links for %s: %w", sid, err)
		}
	}
	return tx.Commit()
}
