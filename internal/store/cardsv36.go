package store

import (
	"database/sql"
	"fmt"
)

const cardsV36 = `CREATE TABLE cards (
    scryfall_id TEXT PRIMARY KEY,
    set_code TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    name TEXT NOT NULL,
    price_usd REAL,
    price_usd_foil REAL,
    scryfall_url TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    mtgjson_uuid TEXT,
    raw_json TEXT,
    rarity TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity')) VIRTUAL,
    set_name TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.set_name')) STORED,
    cmc REAL GENERATED ALWAYS AS (json_extract(raw_json,'$.cmc')) VIRTUAL,
    artist TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.artist')) VIRTUAL,
    released_at TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.released_at')) STORED,
    layout TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.layout')) VIRTUAL,
    type_line TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.type_line'), json_extract(raw_json,'$.card_faces[0].type_line'))) VIRTUAL,
    mana_cost TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.mana_cost'), json_extract(raw_json,'$.card_faces[0].mana_cost'))) STORED,
    oracle_text TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.oracle_text'), json_extract(raw_json,'$.card_faces[0].oracle_text'))) VIRTUAL,
    color_identity TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.color_identity')) STORED,
    power TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.power'), json_extract(raw_json,'$.card_faces[0].power'))) VIRTUAL,
    toughness TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.toughness'), json_extract(raw_json,'$.card_faces[0].toughness'))) VIRTUAL,
    loyalty TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.loyalty'), json_extract(raw_json,'$.card_faces[0].loyalty'))) VIRTUAL,
    flavor_text TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.flavor_text'), json_extract(raw_json,'$.card_faces[0].flavor_text'))) VIRTUAL,
    image_uri TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.image_uris.normal'), json_extract(raw_json,'$.card_faces[0].image_uris.normal'))) VIRTUAL,
    tcgplayer_id INTEGER GENERATED ALWAYS AS (json_extract(raw_json,'$.tcgplayer_id')) VIRTUAL,
    ck_url TEXT,
    ck_foil_url TEXT,
    promo_types TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.promo_types')) STORED,
    tcg_alt_product_id TEXT,
    ck_foil_id TEXT,
    ck_etched_id TEXT,
    tcg_product_id TEXT,
    price_usd_etched REAL,
    tcg_etched_product_id TEXT,
    lang TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.lang')) STORED,
    printed_name TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.printed_name')) VIRTUAL
);`

const cardsV36Columns = `scryfall_id, set_code, collector_number, name, price_usd, price_usd_foil, scryfall_url, updated_at, mtgjson_uuid, raw_json, ck_url, ck_foil_url, tcg_alt_product_id, ck_foil_id, ck_etched_id, tcg_product_id, price_usd_etched, tcg_etched_product_id`

const cardsV36Indexes = `
CREATE INDEX cards_mtgjson_uuid ON cards(mtgjson_uuid);
CREATE INDEX cards_name ON cards(name);
CREATE INDEX cards_set_code ON cards(set_code);
CREATE INDEX cards_trait_filter ON cards(
    type_line, artist, layout, set_name, rarity, cmc, color_identity, scryfall_id);`

func (s *Store) rebuildCardsStored() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("suspending foreign keys: %w", err)
	}
	defer func() { _, _ = s.db.Exec(`PRAGMA foreign_keys = ON`) }()

	if _, err := s.db.Exec(`PRAGMA legacy_alter_table = ON`); err != nil {
		return fmt.Errorf("pinning rename semantics: %w", err)
	}
	defer func() { _, _ = s.db.Exec(`PRAGMA legacy_alter_table = OFF`) }()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DROP TABLE IF EXISTS cards_pre_v36`,
		`DROP INDEX IF EXISTS cards_mtgjson_uuid`,
		`DROP INDEX IF EXISTS cards_name`,
		`DROP INDEX IF EXISTS cards_set_code`,
		`DROP INDEX IF EXISTS cards_trait_filter`,
		`ALTER TABLE cards RENAME TO cards_pre_v36`,
		cardsV36,
		`INSERT INTO cards (` + cardsV36Columns + `)
		 SELECT ` + cardsV36Columns + ` FROM cards_pre_v36`,
		`DROP TABLE cards_pre_v36`,
		cardsV36Indexes,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("rebuilding cards: %w", err)
		}
	}

	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		return fmt.Errorf("rebuilding cards broke a foreign key from %s to %s", table, parent)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.Compact()
}
