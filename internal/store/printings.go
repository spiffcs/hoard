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
		var raw any // nil, not "", so the COALESCE above sees NULL
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

// mergePrintingsTx writes catalog rows that came from another hoard rather
// than from Scryfall, and differs from upsertPrintingsTx in three ways that
// only matter when the source is a sibling database:
//
//   - It keeps the source's updated_at instead of stamping now(), because that
//     timestamp is what says how fresh the prices are, and rewriting it would
//     make a year-old row look freshly fetched.
//   - It refuses to overwrite a newer row (the WHERE on the DO UPDATE). A
//     merge from an old database must not drag the receiving hoard's prices
//     backwards; upsertPrintingsTx overwrites unconditionally because its
//     input is always a live fetch, which is by definition the newest thing
//     anyone has.
//   - It carries mtgjson_uuid, which the Scryfall path never has.
//
// The refusal is silent by design: an older row losing is the correct outcome,
// not an error, and a merge of a stale database is a normal thing to do.
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

	// A second pass fills documents the guard above would have skipped. An
	// older source can still hold a card document the receiving hoard lacks —
	// a decklist import writes catalog rows with no raw_json at all — and
	// losing the freshness contest should not also forfeit the one thing that
	// makes a printing readable. Filling a NULL is never a downgrade.
	fill, err := tx.Prepare(`
UPDATE cards SET raw_json = COALESCE(raw_json, ?),
                 mtgjson_uuid = COALESCE(mtgjson_uuid, ?)
WHERE scryfall_id = ? AND (raw_json IS NULL OR mtgjson_uuid IS NULL)`)
	if err != nil {
		return err
	}
	defer fill.Close()

	for _, p := range ps {
		var raw, uuid any // nil, not "", so the COALESCEs above see NULL
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

// TCGAltProducts returns the split-product mapping: foil and etched hold each
// printing's split TCGplayer product where one exists, stamped every printing
// that has been asked about at all — the resolve gate, so a card with no split
// product is not re-asked forever.
//
// The two are separate maps because they are separate products with separate
// prices. They were once one, and the overlay put both into the foil bucket, so
// a printing with an etched product and no treated foil carried the etched
// price as its foil price.
//
// stamped keys off tcg_alt_product_id alone: v17 wrote it and v21 added the
// etched column beside it, so a printing stamped before v21 has a NULL etched
// id that means "not asked yet" rather than "none". Reading the pair as stamped
// would freeze those printings with no etched id forever.
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

// SaveTCGAltProducts records the split-product ids a set-file read produced,
// including the empty ones — asked-and-none must be distinguishable from
// never-asked. etched may be nil, in which case only the foil ids are written.
func (s *Store) SaveTCGAltProducts(foil, etched map[string]string) error {
	if len(foil) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Both columns in one statement so a printing is never left stamped for
	// one product and unasked for the other, which would read as a gap the
	// resolver keeps trying to fill.
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

// KnownVendorProductIDs reports which printings have been asked about for
// the v20 ids — NULL means never asked, the same stamp convention as the
// links and the alt product id. A pre-v20 card re-reads its set file once so
// the comp sheet can tell "Card Kingdom has no foil product here" from "we
// have not looked yet", which are opposite answers.
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

// VendorProductIDs is the rest of one printing's per-vendor product ids —
// the ones that say which physical product a vendor's price describes.
//
// The treated foil is why they are here. A ripple or surge printing is one
// Scryfall id, so every vendor's foil price is filed under the same key, but
// they are not all selling the same thing under it: TCGplayer splits the
// treated foil into its own listing, while Card Kingdom keeps one foil
// product per printing. CKFoil being present is therefore the evidence that
// Card Kingdom's foil quote is the treated copy and not a base-product price
// wearing the same key. Manapool has no field here because MTGJSON publishes
// no Manapool identifier — which is the whole reason its treated-foil quotes
// cannot be confirmed.
type VendorProductIDs struct {
	TCGProduct string
	CKFoil     string
	CKEtched   string
}

// SaveVendorProductIDs records the per-vendor product ids a set-file read
// produced, empty ones included — asked-and-none must be distinguishable
// from never-asked, the same convention as the links and the alt product id.
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
