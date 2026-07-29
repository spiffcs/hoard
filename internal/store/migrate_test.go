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

// unstampedDB is the state of every hoard that existed before versioning: the
// current tables, but no user_version and no card_prices_alt.
func unstampedDB(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unstamped.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE card_prices_alt`,
		`PRAGMA user_version = 0`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("unstamping: %v", err)
		}
	}
	s.Close()

	s, err = Open(path)
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
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.AddCard(ulamog(), false, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	// Wind it back to the pre-versioning state so reopening has work to do.
	for _, stmt := range []string{`DROP TABLE card_prices_alt`, `PRAGMA user_version = 0`} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("unstamping: %v", err)
		}
	}
	s.Close()

	s, err = Open(path)
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
	cards, err := s.ListCollection()
	if err != nil || len(cards) != 1 || cards[0].QtyNormal != 2 {
		t.Errorf("after migrating: %+v, %v; want the 2 copies intact", cards, err)
	}
}

// v2 splits one combined "normal/foil" label into a column per finish. The
// backfill has to read labels written by v1, including the single-vendor form.
func TestSplitAltSourceBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.AddCardFinish(unpricedFoil(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCard(solRing(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	// Rebuild card_prices_alt in its v1 shape and seed it the way v1 wrote.
	seed := []string{
		`DROP TABLE card_prices_alt`,
		`CREATE TABLE card_prices_alt (
            scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
            mtgjson_uuid TEXT NOT NULL, price_usd REAL, price_usd_foil REAL,
            source TEXT NOT NULL, as_of TEXT NOT NULL)`,
		// Two shops, one per finish.
		`INSERT INTO card_prices_alt VALUES ('ripple-id','u1',0.34,0.49,'tcgplayer/cardkingdom','x')`,
		// One shop for both.
		`INSERT INTO card_prices_alt VALUES ('sol-id','u2',1.00,2.00,'manapool','x')`,
		`PRAGMA user_version = 1`,
	}
	for _, stmt := range seed {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seeding v1 rows: %v", err)
		}
	}
	s.Close()

	s, err = Open(path) // runs v2
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
