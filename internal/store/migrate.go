package store

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type migration struct {
	Version int
	Stmts   string
}

var migrations = []migration{
	{1, schemaV1},
	{2, splitAltPriceSources},
	{3, cacheMTGJSONIDs},
	{4, keepPriceHistory},
	{5, richCardData},
	{6, rememberPriceGaps},
	{7, rememberImports},
	{9, valueSnapshots},
	{10, watchesTable},
	{11, cardFaceDetails},
	{13, bidHistory},
	{14, tcgplayerProductID},
	{15, cardKingdomLinks},
	{16, promoTypesColumn},
	{17, tcgAltProductColumn},
	{18, settingsTable},
	{19, defaultBinderRealName},
	{20, vendorProductIDs},
	{21, etchedPrices},
	{22, cardLanguage},
	{23, holdingCondition},
	{24, finishGuesses},
	{25, guessContainerRepoint},
	{26, guessContainerFK},
	{27, traitFilterIndex},
	{28, percentWatches},
	{29, contradictedPrices},
	{30, purgePhantomFinishSeries},
}

var schemaVersion = migrations[len(migrations)-1].Version

func SchemaVersion() int { return schemaVersion }

const schemaV1 = `
CREATE TABLE IF NOT EXISTS cards (
    scryfall_id      TEXT PRIMARY KEY,
    set_code         TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    name             TEXT NOT NULL,
    price_usd        REAL,
    price_usd_foil   REAL,
    scryfall_url     TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS containers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'manual',
    source_id  TEXT,
    source_url TEXT,
    format     TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(source, source_id)
);

CREATE TABLE IF NOT EXISTS card_entries (
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);

-- Fallback prices for printings Scryfall cannot price. Scryfall's USD figures
-- come from TCGplayer alone, so a printing TCGplayer has no record of is
-- unpriced there; MTGJSON aggregates other vendors and often has it.
--
-- Separate from cards because upsertPrintingsTx rewrites price_usd_foil from
-- every Scryfall response, so a fallback stored there would be nulled out on
-- the very next update-prices run.
CREATE TABLE IF NOT EXISTS card_prices_alt (
    scryfall_id    TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    mtgjson_uuid   TEXT NOT NULL,
    price_usd      REAL,
    price_usd_foil REAL,
    source         TEXT NOT NULL,
    as_of          TEXT NOT NULL
);`

const splitAltPriceSources = `
ALTER TABLE card_prices_alt ADD COLUMN source_usd TEXT;
ALTER TABLE card_prices_alt ADD COLUMN source_usd_foil TEXT;

UPDATE card_prices_alt SET
  source_usd = CASE WHEN price_usd IS NOT NULL THEN
      CASE WHEN instr(source, '/') > 0
           THEN substr(source, 1, instr(source, '/') - 1)
           ELSE source END
  END,
  source_usd_foil = CASE WHEN price_usd_foil IS NOT NULL THEN
      CASE WHEN instr(source, '/') > 0
           THEN substr(source, instr(source, '/') + 1)
           ELSE source END
  END;

ALTER TABLE card_prices_alt DROP COLUMN source;`

const cacheMTGJSONIDs = `
ALTER TABLE cards ADD COLUMN mtgjson_uuid TEXT;

UPDATE cards SET mtgjson_uuid = (
    SELECT a.mtgjson_uuid FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
) WHERE EXISTS (
    SELECT 1 FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
);

CREATE INDEX IF NOT EXISTS cards_mtgjson_uuid ON cards(mtgjson_uuid);`

const keepPriceHistory = `
CREATE TABLE IF NOT EXISTS card_price_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT c.scryfall_id, 'nonfoil',
       COALESCE(c.price_usd, a.price_usd),
       CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
            ELSE COALESCE(a.source_usd, 'fallback') END,
       CASE WHEN c.price_usd IS NOT NULL THEN c.updated_at ELSE a.as_of END
FROM cards c LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id
WHERE COALESCE(c.price_usd, a.price_usd) IS NOT NULL;

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT c.scryfall_id, 'foil',
       COALESCE(c.price_usd_foil, a.price_usd_foil),
       CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
            ELSE COALESCE(a.source_usd_foil, 'fallback') END,
       CASE WHEN c.price_usd_foil IS NOT NULL THEN c.updated_at ELSE a.as_of END
FROM cards c LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id
WHERE COALESCE(c.price_usd_foil, a.price_usd_foil) IS NOT NULL;`

const richCardData = `
ALTER TABLE cards ADD COLUMN raw_json TEXT;

ALTER TABLE cards ADD COLUMN rarity TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity')) VIRTUAL;
ALTER TABLE cards ADD COLUMN set_name TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.set_name')) VIRTUAL;
ALTER TABLE cards ADD COLUMN cmc REAL
    GENERATED ALWAYS AS (json_extract(raw_json,'$.cmc')) VIRTUAL;
ALTER TABLE cards ADD COLUMN artist TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.artist')) VIRTUAL;
ALTER TABLE cards ADD COLUMN released_at TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.released_at')) VIRTUAL;
ALTER TABLE cards ADD COLUMN layout TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.layout')) VIRTUAL;

ALTER TABLE cards ADD COLUMN type_line TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.type_line'),
                                  json_extract(raw_json,'$.card_faces[0].type_line'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN mana_cost TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.mana_cost'),
                                  json_extract(raw_json,'$.card_faces[0].mana_cost'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN oracle_text TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.oracle_text'),
                                  json_extract(raw_json,'$.card_faces[0].oracle_text'))) VIRTUAL;

-- Kept as the JSON array Scryfall sends. Colour is a set, not a string, so
-- filtering reads it with json_each rather than matching substrings — which
-- would make color:U also match a UB card, and color:R match nothing at all
-- when the array happens to be ordered ["B","R"].
ALTER TABLE cards ADD COLUMN color_identity TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.color_identity')) VIRTUAL;

CREATE INDEX IF NOT EXISTS cards_name ON cards(name);

-- card_entries had no index on scryfall_id, so every join from cards — which is
-- most of the valuation queries — scanned the whole table.
CREATE INDEX IF NOT EXISTS card_entries_card_id ON card_entries(scryfall_id);`

const rememberPriceGaps = `
CREATE TABLE IF NOT EXISTS card_price_gaps (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    checked_at  TEXT NOT NULL
);`

const applicationID = 0x484F5244

const rememberImports = `
CREATE TABLE IF NOT EXISTS import_ledger (
    hash        TEXT PRIMARY KEY,
    file        TEXT NOT NULL,
    cards       INTEGER NOT NULL,
    imported_at TEXT NOT NULL
);`

const bidHistory = `
CREATE TABLE card_bid_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);`

const tcgplayerProductID = `
ALTER TABLE cards ADD COLUMN tcgplayer_id INTEGER
    GENERATED ALWAYS AS (json_extract(raw_json,'$.tcgplayer_id')) VIRTUAL;`

const cardKingdomLinks = `
ALTER TABLE cards ADD COLUMN ck_url TEXT;
ALTER TABLE cards ADD COLUMN ck_foil_url TEXT;`

const promoTypesColumn = `
ALTER TABLE cards ADD COLUMN promo_types TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.promo_types')) VIRTUAL;`

const tcgAltProductColumn = `
ALTER TABLE cards ADD COLUMN tcg_alt_product_id TEXT;`

const settingsTable = `
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`

const defaultBinderRealName = `
UPDATE containers SET name = 'Binder' WHERE source_id = '__collection__';`

const vendorProductIDs = `
ALTER TABLE cards ADD COLUMN ck_foil_id TEXT;
ALTER TABLE cards ADD COLUMN ck_etched_id TEXT;
ALTER TABLE cards ADD COLUMN tcg_product_id TEXT;`

const etchedPrices = `
ALTER TABLE cards ADD COLUMN price_usd_etched REAL;
ALTER TABLE cards ADD COLUMN tcg_etched_product_id TEXT;`

const cardLanguage = `
ALTER TABLE cards ADD COLUMN lang TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.lang')) VIRTUAL;
ALTER TABLE cards ADD COLUMN printed_name TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.printed_name')) VIRTUAL;`

const holdingCondition = `
ALTER TABLE card_entries RENAME TO card_entries_pre_v23;

CREATE TABLE card_entries (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    condition    TEXT NOT NULL DEFAULT 'unknown',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    UNIQUE (container_id, scryfall_id, finish, condition, board)
);

INSERT INTO card_entries
    (container_id, scryfall_id, finish, condition, board, quantity)
SELECT container_id, scryfall_id, finish, 'unknown', board, quantity
FROM card_entries_pre_v23;

DROP TABLE card_entries_pre_v23;

CREATE INDEX IF NOT EXISTS card_entries_card_id ON card_entries(scryfall_id);`

const finishGuesses = `
CREATE TABLE finish_guesses (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL,
    guessed_at   TEXT NOT NULL
);
CREATE INDEX finish_guesses_card ON finish_guesses(scryfall_id, finish);`

const guessContainerRepoint = `
UPDATE finish_guesses
SET container_id = (SELECT id FROM containers
                    WHERE source = 'manual' AND source_id = '__collection__')
WHERE container_id = 0
  AND EXISTS (SELECT 1 FROM containers
              WHERE source = 'manual' AND source_id = '__collection__');`

const guessContainerFK = `
ALTER TABLE finish_guesses RENAME TO finish_guesses_pre_v26;

CREATE TABLE finish_guesses (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL,
    guessed_at   TEXT NOT NULL
);

INSERT INTO finish_guesses (id, container_id, scryfall_id, finish, guessed_at)
SELECT id, container_id, scryfall_id, finish, guessed_at
FROM finish_guesses_pre_v26
WHERE container_id IN (SELECT id FROM containers);

DROP TABLE finish_guesses_pre_v26;

CREATE INDEX finish_guesses_card ON finish_guesses(scryfall_id, finish);`

const traitFilterIndex = `
CREATE INDEX IF NOT EXISTS cards_trait_filter ON cards(
    type_line, artist, layout, set_name, rarity, cmc, color_identity, scryfall_id);`

const percentWatches = `
ALTER TABLE watches ADD COLUMN pct           REAL    NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN min_move      REAL    NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN window_days   INTEGER NOT NULL DEFAULT 30;
ALTER TABLE watches ADD COLUMN last_fired_at TEXT    NOT NULL DEFAULT '';`

const contradictedPrices = `
CREATE TABLE IF NOT EXISTS card_price_overrides (
    scryfall_id        TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    price_usd          REAL,
    price_usd_foil     REAL,
    price_usd_etched   REAL,
    refused_usd        REAL,
    refused_usd_foil   REAL,
    refused_usd_etched REAL,
    source             TEXT NOT NULL,
    reason             TEXT NOT NULL,
    as_of              TEXT NOT NULL
);`

const valueSnapshots = `
CREATE TABLE value_snapshots (
    as_of  TEXT PRIMARY KEY,
    binder REAL NOT NULL,
    decks  REAL NOT NULL,
    total  REAL NOT NULL,
    source TEXT NOT NULL DEFAULT 'observed'
);`

const watchesTable = `
CREATE TABLE watches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id),
    display     TEXT NOT NULL,
    finish      TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    op          TEXT NOT NULL,
    threshold   REAL NOT NULL,
    created_at  TEXT NOT NULL,
    last_state  TEXT NOT NULL DEFAULT '',
    UNIQUE(scryfall_id, finish, op)
);`

const cardFaceDetails = `
ALTER TABLE cards ADD COLUMN power TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.power'),
                                  json_extract(raw_json,'$.card_faces[0].power'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN toughness TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.toughness'),
                                  json_extract(raw_json,'$.card_faces[0].toughness'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN loyalty TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.loyalty'),
                                  json_extract(raw_json,'$.card_faces[0].loyalty'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN flavor_text TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.flavor_text'),
                                  json_extract(raw_json,'$.card_faces[0].flavor_text'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN image_uri TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.image_uris.normal'),
                                  json_extract(raw_json,'$.card_faces[0].image_uris.normal'))) VIRTUAL;`

func (s *Store) migrate(path string) error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	if v > schemaVersion {
		return fmt.Errorf(
			"database schema is v%d but this hoard understands v%d. Upgrade hoard; an older build must not write here",
			v, schemaVersion)
	}
	if v == schemaVersion {
		return s.stampApplicationID()
	}

	fresh := false
	if v == 0 {
		var hasCards bool
		if err := s.db.QueryRow(`
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='cards')`).Scan(&hasCards); err != nil {
			return fmt.Errorf("inspecting schema: %w", err)
		}
		fresh = !hasCards
	}
	if !fresh {
		if err := s.backup(path, v); err != nil {
			return err
		}
	}

	if v == 0 {

		if err := s.migrateLegacy(); err != nil {
			return err
		}

		if _, err := s.db.Exec(schemaV1); err != nil {
			return fmt.Errorf("initializing schema: %w", err)
		}
		if err := s.setVersion(1); err != nil {
			return err
		}
		v = 1
	}

	entriesBefore := int64(-1)
	if v < 23 {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_entries`).Scan(&entriesBefore); err != nil {
			entriesBefore = -1
		}
	}

	for _, m := range migrations {
		if m.Version <= v {
			continue
		}
		if err := s.apply(m); err != nil {
			return fmt.Errorf("migrating to schema v%d: %w", m.Version, err)
		}
		if m.Version == 9 {
			if err := s.seedValueSnapshots(); err != nil {
				return fmt.Errorf("seeding value snapshots: %w", err)
			}
		}
	}

	if entriesBefore >= 0 {
		var after int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_entries`).Scan(&after); err != nil {
			return fmt.Errorf("verifying holdings after migration: %w", err)
		}
		if after < entriesBefore {

			return fmt.Errorf(
				"migration left card_entries with %d rows where it had %d; refusing to continue — restore the pre-migration backup beside the database",
				after, entriesBefore)
		}
	}
	return s.stampApplicationID()
}

func (s *Store) seedValueSnapshots() error {
	type key struct{ sid, pfinish string }
	type held struct{ binder, decks int }
	owned := map[key]held{}
	rows, err := s.db.Query(`
SELECT e.scryfall_id,
       CASE WHEN e.finish IN ('foil','etched') THEN 'foil'
            WHEN e.finish = 'nonfoil' THEN 'nonfoil' END AS pfinish,
       CASE WHEN ct.kind = '` + KindCollection + `' THEN 1 ELSE 0 END AS in_binder,
       SUM(e.quantity)
FROM card_entries e
JOIN containers ct ON ct.id = e.container_id
GROUP BY e.scryfall_id, pfinish, in_binder`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k key
		var inBinder, copies int
		if err := rows.Scan(&k.sid, &k.pfinish, &inBinder, &copies); err != nil {
			rows.Close()
			return err
		}
		h := owned[k]
		if inBinder == 1 {
			h.binder += copies
		} else {
			h.decks += copies
		}
		owned[k] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.db.Query(`
SELECT scryfall_id, finish, price_usd, substr(as_of, 1, 10)
FROM card_price_history
WHERE scryfall_id IN (SELECT scryfall_id FROM card_entries)
ORDER BY as_of`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type snapshot struct {
		day           string
		binder, decks float64
	}
	var snaps []snapshot
	cur := map[key]float64{}
	var binderTotal, decksTotal float64
	day := ""
	for rows.Next() {
		var k key
		var price float64
		var d string
		if err := rows.Scan(&k.sid, &k.pfinish, &price, &d); err != nil {
			return err
		}
		if day != "" && d != day {
			snaps = append(snaps, snapshot{day, binderTotal, decksTotal})
		}
		day = d
		h, ok := owned[k]
		if !ok {
			continue
		}
		delta := price - cur[k]
		binderTotal += float64(h.binder) * delta
		decksTotal += float64(h.decks) * delta
		cur[k] = price
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if day != "" {
		snaps = append(snaps, snapshot{day, binderTotal, decksTotal})
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
INSERT INTO value_snapshots (as_of, binder, decks, total, source)
VALUES (?, ?, ?, ?, 'seeded')`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sn := range snaps {
		if _, err := stmt.Exec(sn.day+"T00:00:00Z", sn.binder, sn.decks,
			sn.binder+sn.decks); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) stampApplicationID() error {
	var id int64
	if err := s.db.QueryRow(`PRAGMA application_id`).Scan(&id); err != nil {
		return nil
	}
	if id == applicationID {
		return nil
	}
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA application_id = %d`, applicationID))
	return err
}

func (s *Store) apply(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.Stmts); err != nil {
		return err
	}

	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.Version)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) setVersion(v int) error {
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v))
	if err != nil {
		return fmt.Errorf("stamping schema version: %w", err)
	}
	return nil
}

func (s *Store) backup(path string, fromVersion int) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	dest := fmt.Sprintf("%s.bak-v%d-%s", path, fromVersion, time.Now().Format("20060102"))
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("backing up database before migration: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Backed up database to %s before upgrading schema.\n", dest)
	pruneBackups(path, dest)
	return nil
}

const keptBackups = 3

func pruneBackups(dbPath, keep string) {
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := filepath.Base(dbPath) + ".bak-v"

	type backup struct {
		path    string
		version int
		date    string
	}
	var mine []backup
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == keep {
			continue
		}
		tail := strings.TrimPrefix(e.Name(), prefix)
		ver, date, ok := strings.Cut(tail, "-")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(ver)
		if err != nil {
			continue
		}
		mine = append(mine, backup{full, v, date})
	}
	if len(mine) < keptBackups {
		return
	}

	slices.SortFunc(mine, func(a, b backup) int {
		if c := cmp.Compare(a.version, b.version); c != 0 {
			return c
		}
		return strings.Compare(a.date, b.date)
	})
	for _, old := range mine[:len(mine)-(keptBackups-1)] {
		os.Remove(old.path)
	}
}

func (s *Store) migrateLegacy() error {
	var hasLegacy bool
	err := s.db.QueryRow(`
SELECT EXISTS(
    SELECT 1 FROM pragma_table_info('cards') WHERE name='qty_normal')`).Scan(&hasLegacy)
	if err != nil {
		return fmt.Errorf("checking for legacy schema: %w", err)
	}
	if !hasLegacy {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE cards RENAME TO cards_legacy`); err != nil {
		return fmt.Errorf("migrate: renaming legacy cards: %w", err)
	}
	if _, err := tx.Exec(schemaV1); err != nil {
		return fmt.Errorf("migrate: creating new schema: %w", err)
	}

	ts := now()
	res, err := tx.Exec(`
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, 'Collection', 'manual', ?, ?, ?)`,
		KindCollection, collectionSourceID, ts, ts)
	if err != nil {
		return fmt.Errorf("migrate: creating collection: %w", err)
	}
	cid, err := res.LastInsertId()
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, scryfall_url, updated_at)
SELECT scryfall_id, set_code, collector_number, name,
       price_usd, price_usd_foil, scryfall_url, updated_at
FROM cards_legacy`); err != nil {
		return fmt.Errorf("migrate: copying catalog: %w", err)
	}

	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
SELECT ?, scryfall_id, 'nonfoil', 'main', qty_normal FROM cards_legacy WHERE qty_normal > 0`, cid); err != nil {
		return fmt.Errorf("migrate: copying nonfoil quantities: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
SELECT ?, scryfall_id, 'foil', 'main', qty_foil FROM cards_legacy WHERE qty_foil > 0`, cid); err != nil {
		return fmt.Errorf("migrate: copying foil quantities: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE cards_legacy`); err != nil {
		return fmt.Errorf("migrate: dropping legacy table: %w", err)
	}
	return tx.Commit()
}

const purgePhantomFinishSeries = `
DELETE FROM card_price_history WHERE rowid IN (
  SELECT h.rowid FROM card_price_history h JOIN cards c ON c.scryfall_id = h.scryfall_id
   WHERE h.finish IN ('nonfoil','foil')
     AND json_extract(c.raw_json, '$.finishes') IS NOT NULL
     AND json_extract(c.raw_json, '$.finishes') NOT LIKE '%"' || h.finish || '"%'
     AND NOT (h.finish = 'foil'
              AND json_extract(c.raw_json, '$.finishes') LIKE '%"etched"%'));

DELETE FROM card_bid_history WHERE rowid IN (
  SELECT b.rowid FROM card_bid_history b JOIN cards c ON c.scryfall_id = b.scryfall_id
   WHERE b.finish IN ('nonfoil','foil')
     AND json_extract(c.raw_json, '$.finishes') IS NOT NULL
     AND json_extract(c.raw_json, '$.finishes') NOT LIKE '%"' || b.finish || '"%'
     AND NOT (b.finish = 'foil'
              AND json_extract(c.raw_json, '$.finishes') LIKE '%"etched"%'));`
