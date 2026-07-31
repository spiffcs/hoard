package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	{5, richCardData},
	{6, rememberPriceGaps},
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

// keepPriceHistory remembers what a printing used to cost. Every price write
// before this overwrote in place, so nothing could say what had moved.
//
// Rows are appended only when the price differs from the last observed one: a row
// per card per refresh would grow the database by the size of the catalog daily.
//
// The finish is the *price's* finish, not the one held — etched is valued from the
// foil price (see entryValue), so it shares that series rather than duplicating it.
//
// The seed turns the prices already on disk into a baseline, so the first refresh
// after upgrading has something to compare against.
//
// CAUTION on the CASCADE below: history is the one table that cannot be
// re-fetched — MTGJSON's archive reaches back 90 days, and everything older
// exists only here. Nothing deletes from cards today, so the cascade never
// fires; any future pruning of catalog rows must exempt printings with
// history, or years of observations vanish with a row that was "just a cache".
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

// richCardData keeps the whole Scryfall response and derives columns from it.
//
// Deriving rather than adding a real column per field makes every future field a
// one-line ALTER: VIRTUAL generated columns occupy no space in existing rows, so
// there is no rebuild and no backfill. Costs ~5.4 KB of JSON per card.
//
// No backfill here on purpose — update-prices refetches every owned card, so the
// first run after this fills raw_json. Until then the columns read NULL, which
// callers must show as unknown rather than as zero.
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

// rememberPriceGaps records that MTGJSON was asked about a card and had nothing.
//
// One card costs a 50 MB scan, and cards nothing can price are neither rare nor
// temporary, so without this every refresh pays that scan to re-learn the same
// answer. Only the *absence* is recorded — a price that was found lives in
// card_prices_alt, and a card with one is no longer a gap.
//
// A timestamp rather than a flag: MTGJSON adds vendors, so the check expires and
// the card is asked about again rather than written off.
const rememberPriceGaps = `
CREATE TABLE IF NOT EXISTS card_price_gaps (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    checked_at  TEXT NOT NULL
);`

// applicationID marks the file as a hoard database in its SQLite header:
// "HORD" in ASCII, per the application-file-format convention, so a tool (or a
// future hoard command) can tell a hoard from any other .db without guessing
// at table names.
const applicationID = 0x484F5244

// migrate brings the database up to schemaVersion, backing it up first.
//
// The backup fires before anything rewrites data — including the legacy
// single-table transform below — because a backup taken after a transform can
// only preserve the transform's mistakes.
func (s *Store) migrate(path string) error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	// A file from a newer hoard is refused, not written. Every migration so
	// far has been additive, but that is circumstance rather than contract,
	// and this is the one file that cannot be re-downloaded after an older
	// binary scribbles on it.
	if v > schemaVersion {
		return fmt.Errorf(
			"database schema is v%d but this hoard understands v%d — upgrade hoard; an older build must not write here",
			v, schemaVersion)
	}
	if v == schemaVersion {
		return s.stampApplicationID()
	}

	// Every hoard predating versioning reports user_version 0, and three
	// states share that value: an empty file, a database from the original
	// single-table build, and a v1 database that was never marked. Only the
	// empty file is fresh — a database created moments ago has nothing worth
	// preserving, and backing it up would leave a stray file beside every new
	// hoard.
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
		// The original build's wide cards table, migrated in place — after the
		// backup above, so its pre-state survives on disk.
		if err := s.migrateLegacy(); err != nil {
			return err
		}
		// Safe on all three v0 states: creates what is missing, touches what
		// is not.
		if _, err := s.db.Exec(schemaV1); err != nil {
			return fmt.Errorf("initializing schema: %w", err)
		}
		if err := s.setVersion(1); err != nil {
			return err
		}
		v = 1
	}

	for _, m := range migrations {
		if m.Version <= v {
			continue
		}
		if err := s.apply(m); err != nil {
			return fmt.Errorf("migrating to schema v%d: %w", m.Version, err)
		}
	}
	return s.stampApplicationID()
}

// stampApplicationID writes hoard's magic into the file header, once. Failure
// to read the pragma is not worth failing Open over — the id is a courtesy to
// tooling, not a load-bearing invariant.
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
	pruneBackups(path, dest)
	return nil
}

// keptBackups is how many pre-migration snapshots survive a prune.
//
// One is the actual safety net — the state immediately before the migration you
// just ran. The extra two cover the case where a migration's damage is only
// noticed after the next one has already been applied.
const keptBackups = 3

// pruneBackups removes all but the most recent snapshots of one database, since
// they otherwise accumulate one per migration forever and are not small.
//
// keep is the current run's backup, never a removal candidate. The match is
// deliberately narrow — the exact `.bak-v<n>-<date>` shape written here — so a
// hand-made backup is not swept up by a cleanup nobody asked for. Best-effort:
// not worth failing a migration over.
func pruneBackups(dbPath, keep string) {
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := filepath.Base(dbPath) + ".bak-v"

	var mine []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == keep {
			continue
		}
		mine = append(mine, full)
	}
	if len(mine) < keptBackups {
		return
	}

	// The names embed version then date, both zero-padded enough to sort in the
	// order they were written, so the oldest are simply the first.
	slices.Sort(mine)
	for _, old := range mine[:len(mine)-(keptBackups-1)] {
		os.Remove(old)
	}
}

// migrateLegacy upgrades a database created by the original single-table build
// (a `cards` table with a qty_normal column) to the current normalized schema.
// It is a no-op on fresh databases and on already-migrated ones.
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

	// Copy identity + prices into the new catalog.
	if _, err := tx.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, scryfall_url, updated_at)
SELECT scryfall_id, set_code, collector_number, name,
       price_usd, price_usd_foil, scryfall_url, updated_at
FROM cards_legacy`); err != nil {
		return fmt.Errorf("migrate: copying catalog: %w", err)
	}
	// Move quantities into collection entries (one row per non-zero finish).
	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
SELECT ?, scryfall_id, 'normal', 'main', qty_normal FROM cards_legacy WHERE qty_normal > 0`, cid); err != nil {
		return fmt.Errorf("migrate: copying normal quantities: %w", err)
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
