package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// schemaDump is every table and index in the database, normalized for comparison.
func schemaDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
SELECT COALESCE(sql, '') FROM sqlite_master
WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("reading sqlite_master: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, strings.Join(strings.Fields(s), " "))
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

// freshDB is a database created by the current code from nothing.
func freshDB(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// preVersioningDDL is the schema as it stood before any of this existed: three
// tables, no card_prices_alt, no user_version.
//
// Frozen here on purpose. Building the fixture by degrading a current database
// produces states that never occurred in the wild, such as a cards table
// already carrying a column added by a migration it has not run yet.
const preVersioningDDL = `
CREATE TABLE cards (
    scryfall_id      TEXT PRIMARY KEY,
    set_code         TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    name             TEXT NOT NULL,
    price_usd        REAL,
    price_usd_foil   REAL,
    scryfall_url     TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE TABLE containers (
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
CREATE TABLE card_entries (
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL DEFAULT 'normal',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);
PRAGMA user_version = 0;`

// seedRawDB writes DDL straight to a new file, bypassing Open so no migration
// runs.
func seedRawDB(t *testing.T, path, ddl string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}
}

// unstampedDB is a hoard from before versioning, migrated forward by Open.
func unstampedDB(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unstamped.db")
	seedRawDB(t, path, preVersioningDDL)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen unstamped: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// TestMigrationsConverge is the test that matters: however a database got here,
// it must end up with byte-identical structure. A bug that leaves one path a
// column short is otherwise invisible until a query fails at runtime.
func TestMigrationsConverge(t *testing.T) {
	fresh := freshDB(t)
	unstamped, _ := unstampedDB(t)

	wantSchema := schemaDump(t, fresh.db)
	if got := schemaDump(t, unstamped.db); got != wantSchema {
		t.Errorf("unstamped database migrated to a different schema:\n got %s\nwant %s",
			got, wantSchema)
	}
	if v := userVersion(t, fresh.db); v != schemaVersion {
		t.Errorf("fresh user_version = %d, want %d", v, schemaVersion)
	}
	if v := userVersion(t, unstamped.db); v != schemaVersion {
		t.Errorf("unstamped user_version = %d, want %d", v, schemaVersion)
	}
	// The split columns must exist on both, since that is what v2 adds. Checked
	// against card_prices_alt specifically: `containers` has its own unrelated
	// `source` column, so searching the whole dump gives a false positive.
	for _, s := range []*Store{fresh, unstamped} {
		cols := altPriceColumns(t, s.db)
		for _, want := range []string{"source_usd", "source_usd_foil"} {
			if !cols[want] {
				t.Errorf("card_prices_alt is missing %s; has %v", want, cols)
			}
		}
		if cols["source"] {
			t.Errorf("the combined source column should have been dropped; has %v", cols)
		}
	}
}

// v3 moves the Scryfall-to-MTGJSON id map onto the card, where it survives the
// nightly cache prune. Losing an id in the migration would mean re-downloading
// that card's whole set file.
func TestMTGJSONIDBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, schemaV1+`
INSERT INTO cards VALUES ('has-id','m3c','218','Acidic Slime',0.34,NULL,'http://x','x');
INSERT INTO cards VALUES ('no-id','c21','1','Sol Ring',2.0,5.0,'http://x','x');
INSERT INTO card_prices_alt VALUES ('has-id','uuid-abc',0.34,0.49,'cardkingdom','x');
PRAGMA user_version = 1;`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	ids, err := s.KnownMTGJSONUUIDs()
	if err != nil {
		t.Fatalf("KnownMTGJSONUUIDs: %v", err)
	}
	if ids["has-id"] != "uuid-abc" {
		t.Errorf("ids = %v, want the id carried over from card_prices_alt", ids)
	}
	// A card that never needed a fallback price simply has no id yet.
	if _, ok := ids["no-id"]; ok {
		t.Errorf("ids = %v, want no entry for a card that never had one", ids)
	}

	// Newly resolved ids must stick, so the set file is fetched once ever.
	if err := s.SaveMTGJSONUUIDs(map[string]string{"no-id": "uuid-def", "": "ignored"}); err != nil {
		t.Fatalf("SaveMTGJSONUUIDs: %v", err)
	}
	ids, _ = s.KnownMTGJSONUUIDs()
	if ids["no-id"] != "uuid-def" || len(ids) != 2 {
		t.Errorf("ids = %v, want both cards resolved", ids)
	}
}

func altPriceColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info('card_prices_alt')`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[n] = true
	}
	return out
}

// Reopening an already-current database must do nothing at all: no migration,
// no backup file, no version change.
func TestMigrationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hoard.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	before := schemaDump(t, s.db)
	s.Close()

	for range 3 {
		s, err = Open(path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if got := schemaDump(t, s.db); got != before {
			t.Errorf("reopen changed the schema:\n got %s\nwant %s", got, before)
		}
		s.Close()
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("reopening an up-to-date database left a backup: %s", e.Name())
		}
	}
}

// A migration on a database with real data must back it up first, since a hoard
// is not re-downloadable.
func TestMigrationBacksUpExistingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hoard.db")
	// A pre-versioning hoard with data in it, so migrating has something to
	// protect.
	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',
                          10.0,25.0,'http://x','2020-01-01T00:00:00Z');
INSERT INTO containers (kind,name,source,source_id,created_at,updated_at)
  VALUES ('collection','Collection','manual','__collection__','x','x');
INSERT INTO card_entries VALUES (1,'ulamog-id','normal','main',2);`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()

	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	// The data must have survived the migration.
	if held := heldByFinish(t, s, "ulamog-id"); held["normal"] != 2 {
		t.Errorf("after migrating: %v; want the 2 copies intact", held)
	}
}

// v2 splits one combined "normal/foil" label into a column per finish. The
// backfill has to read labels written by v1, including the single-vendor form.
func TestSplitAltSourceBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	// A database frozen at v1, built from the frozen v1 schema rather than by
	// degrading a current one, so it stays a valid fixture as migrations pile up.
	seedRawDB(t, path, schemaV1+`
INSERT INTO cards VALUES ('ripple-id','m3c','218','Acidic Slime',
                          0.34,NULL,'http://x','x');
INSERT INTO cards VALUES ('sol-id','c21','1','Sol Ring',2.0,5.0,'http://x','x');
-- Two shops, one per finish, written the way v1 wrote them.
INSERT INTO card_prices_alt VALUES ('ripple-id','u1',0.34,0.49,'tcgplayer/cardkingdom','x');
-- One shop for both.
INSERT INTO card_prices_alt VALUES ('sol-id','u2',1.00,2.00,'manapool','x');
PRAGMA user_version = 1;`)

	s, err := Open(path) // runs v2 and v3
	if err != nil {
		t.Fatalf("reopen (migrate to v2): %v", err)
	}
	defer s.Close()

	var normal, foil sql.NullString
	err = s.db.QueryRow(`SELECT source_usd, source_usd_foil FROM card_prices_alt
                         WHERE scryfall_id='ripple-id'`).Scan(&normal, &foil)
	if err != nil {
		t.Fatalf("reading split sources: %v", err)
	}
	if normal.String != "tcgplayer" || foil.String != "cardkingdom" {
		t.Errorf("split = %q/%q, want tcgplayer/cardkingdom", normal.String, foil.String)
	}

	err = s.db.QueryRow(`SELECT source_usd, source_usd_foil FROM card_prices_alt
                         WHERE scryfall_id='sol-id'`).Scan(&normal, &foil)
	if err != nil {
		t.Fatalf("reading single-vendor sources: %v", err)
	}
	if normal.String != "manapool" || foil.String != "manapool" {
		t.Errorf("single vendor = %q/%q, want manapool for both", normal.String, foil.String)
	}
}
