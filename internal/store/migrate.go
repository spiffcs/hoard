package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// migration is one versioned schema change.
//
// Once a migration ships its Stmts are frozen forever: databases in the wild
// have already run them, so editing one changes history rather than the schema.
// New changes go on the end as a new version.
type migration struct {
	Version int
	Stmts   string
}

// migrations are applied in order to any database behind schemaVersion.
//
// Version 1 is the baseline every hoard already has. It keeps IF NOT EXISTS
// because it is also what bootstraps a database that predates versioning, where
// some of those tables exist and some do not. Every later migration is an
// explicit ALTER: a whole-schema blob cannot add a column to a table that
// already exists, and failing silently is exactly the trap this runner exists to
// close.
var migrations = []migration{
	{1, schemaV1},
	{2, splitAltPriceSources},
	{3, cacheMTGJSONIDs},
	{4, keepPriceHistory},
}

// schemaVersion is the version a database is brought up to.
var schemaVersion = migrations[len(migrations)-1].Version

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
    finish       TEXT NOT NULL DEFAULT 'normal',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);

-- Fallback prices for printings Scryfall cannot price. Scryfall's USD figures
-- come from TCGplayer alone, so a printing TCGplayer has no record of is
-- unpriced there; MTGJSON aggregates other vendors and often has it.
--
-- Separate from cards because upsertCatalogTx rewrites price_usd_foil from
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

// splitAltPriceSources records which vendor priced each finish.
//
// A card's two finishes can come from different shops, and v1 crammed that into
// one column as "normal/foil". That reads wrong on a per-finish row: a foil line
// would name the vendor that supplied the non-foil price. The backfill splits
// existing labels on the same "/" that wrote them.
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

// cacheMTGJSONIDs keeps the Scryfall-to-MTGJSON id map on the card itself.
//
// The map previously existed only as a side effect of card_prices_alt, which is
// written solely for cards that had a price gap, so most owned cards had no id
// cached. Resolving one means downloading that card's whole set file, and the
// download cache is pruned daily, so anything reading prices across the entire
// collection would re-fetch dozens of set files every day it ran.
//
// The id is a property of the printing and never changes, so it belongs beside
// the printing and is worth keeping forever.
const cacheMTGJSONIDs = `
ALTER TABLE cards ADD COLUMN mtgjson_uuid TEXT;

UPDATE cards SET mtgjson_uuid = (
    SELECT a.mtgjson_uuid FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
) WHERE EXISTS (
    SELECT 1 FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
);

CREATE INDEX IF NOT EXISTS cards_mtgjson_uuid ON cards(mtgjson_uuid);`

// keepPriceHistory remembers what a printing used to cost.
//
// Every price write before this was destructive: upsertCatalogTx and
// UpsertAltPrices both overwrite in place, so the moment a refresh committed,
// the old number was gone and nothing could say what had moved. cards.updated_at
// records when a price was last written, never what it was.
//
// Rows are appended only when the price actually differs from the last one
// observed, which is what keeps the table small: most of a collection does not
// move on a given day, and a row per card per refresh would grow the database by
// the size of the catalog every time.
//
// The finish here is the price's finish — 'normal' or 'foil' — not the finish a
// card is held in. Etched copies are valued from the foil price (see
// entryValue), so they share its history rather than getting a third series that
// would always duplicate it.
//
// The seed backfills one observation per priced card from the prices already
// stored, timestamped when they were actually written. Without it the first
// refresh after upgrading would have nothing to compare against and would report
// no movement at all; with it, the numbers already on disk become the baseline.
const keepPriceHistory = `
CREATE TABLE IF NOT EXISTS card_price_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL,
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT c.scryfall_id, 'normal',
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

// migrate brings the database up to schemaVersion, backing it up first.
func (s *Store) migrate(path string) error {
	v, fresh, err := s.bootstrapVersion()
	if err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	// A database created moments ago has nothing worth preserving, and backing
	// it up would leave a stray file beside every new hoard.
	if !fresh {
		if err := s.backup(path, v); err != nil {
			return err
		}
	}
	for _, m := range migrations {
		if m.Version <= v {
			continue
		}
		if err := s.apply(m); err != nil {
			return fmt.Errorf("migrating to schema v%d: %w", m.Version, err)
		}
	}
	return nil
}

// bootstrapVersion returns the database's schema version, stamping it first if
// it predates versioning.
//
// Every hoard in existence reports user_version 0, and three different states
// share that value, so they have to be told apart before anything is stamped:
// an empty file, a database from the original single-table build, and a current
// one that simply was never marked. This is the last place pragma_table_info is
// consulted to work out a version.
func (s *Store) bootstrapVersion() (version int, fresh bool, err error) {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return 0, false, fmt.Errorf("reading schema version: %w", err)
	}
	if v > 0 {
		return v, false, nil
	}

	// An empty file has no tables at all, which is what separates a brand-new
	// hoard from one that simply predates versioning.
	var hasCards bool
	if err := s.db.QueryRow(`
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='cards')`).Scan(&hasCards); err != nil {
		return 0, false, fmt.Errorf("inspecting schema: %w", err)
	}

	// The original build's wide cards table, migrated in place.
	if err := s.migrateLegacy(); err != nil {
		return 0, false, err
	}
	// Safe on all three states: creates what is missing, touches what is not.
	if _, err := s.db.Exec(schemaV1); err != nil {
		return 0, false, fmt.Errorf("initializing schema: %w", err)
	}
	if err := s.setVersion(1); err != nil {
		return 0, false, err
	}
	return 1, !hasCards, nil
}

// apply runs one migration and stamps its version in the same transaction.
// SQLite keeps user_version in the file header, so it rolls back with the rest
// and a half-applied migration cannot look complete.
func (s *Store) apply(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.Stmts); err != nil {
		return err
	}
	// PRAGMA does not take bind parameters, and Version is an int constant
	// from this file rather than anything a caller supplies.
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

// backup copies the database before a migration touches it.
//
// VACUUM INTO is used rather than copying the file: it is atomic, it accounts
// for any journal or WAL content, and it needs no second connection. A hoard is
// not re-downloadable, so this is worth the disk.
func (s *Store) backup(path string, fromVersion int) error {
	if path == "" || path == ":memory:" {
		return nil // in-memory databases, i.e. tests
	}
	dest := fmt.Sprintf("%s.bak-v%d-%s", path, fromVersion, time.Now().Format("20060102"))
	if _, err := os.Stat(dest); err == nil {
		return nil // already backed up at this version today
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite, which is why the stat above matters.
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("backing up database before migration: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Backed up database to %s before upgrading schema.\n", dest)
	return nil
}
