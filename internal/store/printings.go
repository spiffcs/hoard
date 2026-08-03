package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// UpsertPrintings inserts or refreshes printings (identity + price).
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
	// raw_json coalesces to what is already stored, so a card written from a
	// source that carries no response — a decklist import, a test fixture —
	// leaves the document a previous refresh fetched intact rather than nulling
	// it and silently emptying every generated column that depends on it.
	stmt, err := tx.Prepare(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, scryfall_url, updated_at, raw_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    set_code         = excluded.set_code,
    collector_number = excluded.collector_number,
    name             = excluded.name,
    price_usd        = excluded.price_usd,
    price_usd_foil   = excluded.price_usd_foil,
    scryfall_url     = excluded.scryfall_url,
    updated_at       = excluded.updated_at,
    raw_json         = COALESCE(excluded.raw_json, cards.raw_json)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, c := range cards {
		var raw any // nil, not "", so the COALESCE above sees NULL
		if len(c.Raw) > 0 {
			raw = string(c.Raw)
		}
		if _, err := stmt.Exec(c.ID, c.Set, c.CollectorNumber, c.Name,
			c.PriceUSD, c.PriceUSDFoil, c.ScryfallURL, ts, raw); err != nil {
			return fmt.Errorf("upserting catalog card %s: %w", c.Name, err)
		}
	}
	return nil
}

// ActivePrintingIDs returns every printing that is held or watched — the
// bulk-refresh set. Not every row in cards: re-pointing entries at
// corrected printings (deck repin, the detail's set editor) leaves the old
// rows behind as orphans, and refreshing those spends network on cards
// nobody owns — observed live as a permanent end-of-bar stall when a few
// orphans post-dated the catalog bundle and hit the API on every run.
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

// IDsNeedingDocuments lists catalogued printings with no stored Scryfall
// document.
//
// The local catalog carries prices and identity but not the whole response, so a
// refresh served from it cannot fill raw_json — and the descriptive columns
// derived from it would stay null forever for any card added before migration v5
// or imported from a decklist. This is the bounded set that still has to come
// from the API, and it empties after one refresh rather than recurring.
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

// KnownMTGJSONUUIDs returns the Scryfall-ID-to-UUID pairs already resolved, so a
// set file is downloaded at most once ever.
//
// The ids live on the card rather than beside a price, because resolving one
// costs a whole set-file download and the answer never changes. Keeping them
// only for cards that happened to need a fallback price would make any
// collection-wide price read re-download most of the catalog's set files.
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

// SaveMTGJSONUUIDs records resolved ids so their set files are never fetched
// again.
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

// CKLinks is Card Kingdom's sanctioned product links for one printing —
// one per finish. Empty strings are meaningful: they record that the set
// file was read and had no link, which is what stops absence from
// re-fetching the file forever.
type CKLinks struct {
	URL     string
	FoilURL string
}

// KnownCardKingdomLinks reports which printings have been asked about —
// NULL means never asked, anything else (a link or the recorded absence)
// means the set file need not be fetched again for this card.
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

// TCGAltProducts returns the treated-product mapping: ids holds each
// printing's split TCGplayer product where one exists, stamped every
// printing that has been asked about at all — the resolve gate, so a card
// with no split product is not re-asked forever.
func (s *Store) TCGAltProducts() (ids map[string]string, stamped map[string]bool, err error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, tcg_alt_product_id FROM cards WHERE tcg_alt_product_id IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ids, stamped = map[string]string{}, map[string]bool{}
	for rows.Next() {
		var sid, pid string
		if err := rows.Scan(&sid, &pid); err != nil {
			return nil, nil, err
		}
		stamped[sid] = true
		if pid != "" {
			ids[sid] = pid
		}
	}
	return ids, stamped, rows.Err()
}

// SaveTCGAltProducts records the treated-product ids a set-file read
// produced, including the empty ones — asked-and-none must be
// distinguishable from never-asked.
func (s *Store) SaveTCGAltProducts(ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE cards SET tcg_alt_product_id = ? WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, pid := range ids {
		if _, err := stmt.Exec(pid, sid); err != nil {
			return fmt.Errorf("caching tcg product id for %s: %w", sid, err)
		}
	}
	return tx.Commit()
}

// SaveCardKingdomLinks records the links a set-file read produced,
// including the empty ones — asked-and-none must be distinguishable from
// never-asked.
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
