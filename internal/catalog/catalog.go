package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 6

const fileName = "catalog.db"

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

const (
	keySchema        = "schema_version"
	keySourceUpdated = "source_updated_at"
	keyBuilt         = "built_at"
	keyChecked       = "last_checked_at"
	keyCards         = "card_count"
)

type Catalog struct {
	db   *sql.DB
	dir  string
	path string

	entry *bundle

	replacedOutdated bool
}

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

func (c *Catalog) ReplacedOutdated() bool { return c.replacedOutdated }

func openAt(dir, path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("catalog: opening %s: %w", path, err)
	}

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

func (c *Catalog) Close() error { return c.db.Close() }

func (c *Catalog) Path() string { return c.path }

func (c *Catalog) CardCount() int {
	n, _ := c.metaInt(keyCards)
	return n
}

func (c *Catalog) built() time.Time { return c.metaTime(keyBuilt) }

func (c *Catalog) sourceUpdated() time.Time { return c.metaTime(keySourceUpdated) }

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
