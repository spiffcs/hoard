// Package catalog keeps a local copy of Scryfall's card data, so lookups that
// were API calls become queries against a file. The shape is a vulnerability
// scanner's: check a small listing, download only when it is newer, build
// locally, swap atomically.
//
// Two consequences of it being a cache rather than a store, both load-bearing:
// it lives in the OS cache directory and never inside hoard.db, since everything
// here is re-downloadable and a collection is not; and it has no migrations,
// because a schema change rebuilds instead of upgrading.
//
// Advisory, never authoritative: callers fall through to the API on a miss, so a
// card printed since the last build is still addable.
package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// schemaVersion is bumped whenever the tables below change. A mismatch rebuilds
// the catalog from scratch — the data is a download away, so there is nothing to
// preserve and no migration to write.
//
// v2 added colors and color_identity, so the add-flow picker can show
// identity pips for cards not yet owned.
//
// v3 added lang, so a scan that reads a card's language can tell a
// foreign-only printing from its English namesake — they share a set and a
// collector number, differing only by a marker the OCR cannot see.
const schemaVersion = 5

// fileName is the catalog's name inside the cache directory.
const fileName = "catalog.db"

// schema is the whole catalog.
//
// It stores what a *lookup* needs and nothing more. The card detail pane reads
// type lines, oracle text and mana values — but only for cards you own, and
// those already carry the full Scryfall document in hoard.db's raw_json. Copying
// them for a hundred thousand printings would roughly double this file to serve
// rows nothing reads.
const schema = `
CREATE TABLE cards (
    scryfall_id      TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    name_norm        TEXT NOT NULL,
    set_code         TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    set_name         TEXT,
    released_at      TEXT,
    rarity           TEXT,
    -- Scryfall's two- or three-letter language code ("en", "ja", "zhs").
    --
    -- The bundle is default_cards, so this is "en" for almost everything: one
    -- row per printing, English where an English printing exists. It earns its
    -- place on the rows where it is not — a printing published only in another
    -- language, like the War of the Spark Japanese alternate-art planeswalkers,
    -- which sit beside their English namesake under a marked collector number
    -- and trade for many times as much. The scanner reads the language off the
    -- card's set row, and this is what it matches against.
    lang             TEXT,
    finishes         TEXT,
    promo_types      TEXT,
    frame_effects    TEXT,
    -- Scryfall's frame family ("1993", "1997", "2015", …): what separates a
    -- retro-frame reprint from its regular same-set twin for the scanner.
    frame            TEXT,
    border_color     TEXT,
    -- Scryfall's small-size image URL (front face on multi-faced cards).
    image_uri        TEXT,
    colors           TEXT,
    color_identity   TEXT,
    price_usd        REAL,
    price_usd_foil   REAL,
    price_usd_etched REAL,
    scryfall_url     TEXT NOT NULL
);
CREATE INDEX cards_name_norm ON cards(name_norm);
CREATE INDEX cards_set_num   ON cards(set_code, collector_number);

-- Fuzzy matching resolves text to a *name*; the existing cascade then resolves
-- the name to printings. Magic has far fewer distinct names than printings, so
-- indexing names rather than cards keeps this table a fraction of the size.
CREATE TABLE names (
    name_norm TEXT PRIMARY KEY,
    name      TEXT NOT NULL
);
CREATE TABLE name_trigrams (
    tri       TEXT NOT NULL,
    name_norm TEXT NOT NULL
);
CREATE INDEX name_trigrams_tri ON name_trigrams(tri);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`

// Meta keys.
const (
	keySchema        = "schema_version"
	keySourceUpdated = "source_updated_at" // the bulk file's own timestamp
	keyBuilt         = "built_at"
	keyChecked       = "last_checked_at"
	keyCards         = "card_count"
)

// Catalog is an open local catalog.
type Catalog struct {
	db   *sql.DB
	dir  string
	path string
	// entry memoizes the bulk-data listing for this process. Freshness, size
	// and the download URL are three questions about the same few KB of JSON,
	// and asking per question meant one update-prices fetched it four times.
	entry *bundle
	// replacedOutdated marks that Open discarded a populated catalog whose
	// schema predates this build — see ReplacedOutdated.
	replacedOutdated bool
}

// Open opens the catalog in dir, creating an empty one if there is none.
//
// Anything unusable is replaced rather than repaired — a catalog from an older
// schema, a truncated file, or something else entirely that happens to share the
// name. This lives in a cache directory that any tool may write to, and every
// byte of it is a download away, so starting over is always both safe and
// cheaper than the code to avoid it.
func Open(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("catalog: no cache directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("catalog: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, fileName)

	replacedOutdated := false
	if c, err := openAt(dir, path); err == nil {
		if v, _ := c.metaInt(keySchema); v == schemaVersion {
			return c, nil
		}
		// A populated catalog in an older format is about to be discarded.
		// Remember that, so the caller can say why the catalog is suddenly
		// empty — a silent wipe reads as data loss, and the next update
		// being a full ~100k-card rebuild deserves a sentence.
		replacedOutdated = c.CardCount() > 0
		c.db.Close()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("catalog: replacing unusable catalog: %w", err)
	}
	c, err := openAt(dir, path)
	if err != nil {
		return nil, err
	}
	c.replacedOutdated = replacedOutdated
	return c, nil
}

// ReplacedOutdated reports that Open discarded a populated catalog built
// with an older schema version; the next update rebuilds it in full.
func (c *Catalog) ReplacedOutdated() bool { return c.replacedOutdated }

// openAt opens or creates the database at path and ensures the schema exists.
func openAt(dir, path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("catalog: opening %s: %w", path, err)
	}
	// One connection, matching internal/store: a PRAGMA has to land on the same
	// connection as the statements it configures.
	db.SetMaxOpenConns(1)

	c := &Catalog{db: db, dir: dir, path: path}
	var n int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cards'`).Scan(&n)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("catalog: reading %s: %w", path, err)
	}
	if n == 0 {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, fmt.Errorf("catalog: creating schema: %w", err)
		}
		if err := c.setMeta(keySchema, fmt.Sprint(schemaVersion)); err != nil {
			db.Close()
			return nil, err
		}
	}
	return c, nil
}

// Close releases the database handle.
func (c *Catalog) Close() error { return c.db.Close() }

// Path is where the catalog lives, for a status report.
func (c *Catalog) Path() string { return c.path }

// CardCount is how many printings are stored. Zero means the catalog has never
// been built, which callers should treat as "fall through to the API for
// everything" rather than as "no such card".
func (c *Catalog) CardCount() int {
	n, _ := c.metaInt(keyCards)
	return n
}

// Built is when the catalog was last built, zero if never.
func (c *Catalog) built() time.Time { return c.metaTime(keyBuilt) }

// SourceUpdated is the timestamp the bulk file itself carried.
func (c *Catalog) sourceUpdated() time.Time { return c.metaTime(keySourceUpdated) }

// Bytes is the catalog's size on disk, 0 if it cannot be measured.
func (c *Catalog) Bytes() int64 {
	fi, err := os.Stat(c.path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (c *Catalog) setMeta(key, value string) error {
	_, err := c.db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("catalog: writing %s: %w", key, err)
	}
	return nil
}

func (c *Catalog) meta(key string) string {
	var v string
	if err := c.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func (c *Catalog) metaInt(key string) (int, bool) {
	var v int
	if _, err := fmt.Sscanf(c.meta(key), "%d", &v); err != nil {
		return 0, false
	}
	return v, true
}

func (c *Catalog) metaTime(key string) time.Time {
	t, err := time.Parse(time.RFC3339, c.meta(key))
	if err != nil {
		return time.Time{}
	}
	return t
}
