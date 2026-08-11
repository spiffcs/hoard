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

// v12 repairs the history rows the backfill wrote in MTGJSON's finish
// vocabulary after v8's one-time rename: they move to the store's word, a
// same-day collision keeps the row that was there first, and nothing stays
// behind under "normal".
func TestMigrateRenormalizesHistoryFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, schemaV1+`
CREATE TABLE card_price_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL,
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);
`+
		// A real v11 database ran v5, whose columns later migrations build on
		// (v14's generated column, v27's index), so the fixture carries it whole.
		// Same for v10's watches table, which v28 adds columns to.
		richCardData+watchesTable+`
INSERT INTO cards (scryfall_id, set_code, collector_number, name, price_usd,
                   price_usd_foil, scryfall_url, updated_at)
VALUES ('c1','mh3','1','Fblthp',1.0,NULL,'http://x','x');
INSERT INTO card_price_history VALUES ('c1','nonfoil',10.0,'scryfall','2026-07-10T05:00:00Z');
INSERT INTO card_price_history VALUES ('c1','normal',9.9,'tcgplayer','2026-07-10T05:00:00Z');
INSERT INTO card_price_history VALUES ('c1','normal',8.0,'tcgplayer','2026-07-01T00:00:00Z');
PRAGMA user_version = 11;`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	rows, err := s.db.Query(`SELECT finish, price_usd, as_of FROM card_price_history ORDER BY as_of`)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	defer rows.Close()
	type h struct {
		finish, asOf string
		price        float64
	}
	var got []h
	for rows.Next() {
		var r h
		if err := rows.Scan(&r.finish, &r.price, &r.asOf); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	want := []h{
		{"nonfoil", "2026-07-01T00:00:00Z", 8.0},
		{"nonfoil", "2026-07-10T05:00:00Z", 10.0},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("history after v12 = %+v, want %+v", got, want)
	}
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
	if held := heldByFinish(t, s, "ulamog-id"); held["nonfoil"] != 2 {
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

// The generated columns are the whole point of v5: without them there is nothing
// to filter a collection by. This asserts they resolve from a real Scryfall
// document rather than merely existing.
func TestRichCardDataGeneratedColumns(t *testing.T) {
	s := freshDB(t)

	const bitterblossom = `{"id":"bb","name":"Bitterblossom","set":"uma",
	  "set_name":"Ultimate Masters","collector_number":"85","rarity":"mythic",
	  "type_line":"Tribal Enchantment — Faerie","mana_cost":"{1}{B}","cmc":2.0,
	  "oracle_text":"At the beginning of your upkeep...","artist":"Jesper Ejsing",
	  "released_at":"2018-12-07","layout":"nonfoil","color_identity":["B"]}`

	if _, err := s.db.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name, scryfall_url, updated_at, raw_json)
VALUES ('bb','uma','85','Bitterblossom','http://x','2026-07-30T00:00:00Z', ?)`,
		bitterblossom); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var rarity, typeLine, mana, artist, setName, layout, colors string
	var cmc float64
	if err := s.db.QueryRow(`
SELECT rarity, type_line, mana_cost, artist, set_name, layout, color_identity, cmc
FROM cards WHERE scryfall_id = 'bb'`).
		Scan(&rarity, &typeLine, &mana, &artist, &setName, &layout, &colors, &cmc); err != nil {
		t.Fatalf("reading generated columns: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"rarity", rarity, "mythic"},
		{"type_line", typeLine, "Tribal Enchantment — Faerie"},
		{"mana_cost", mana, "{1}{B}"},
		{"artist", artist, "Jesper Ejsing"},
		{"set_name", setName, "Ultimate Masters"},
		{"layout", layout, "nonfoil"},
		{"color_identity", colors, `["B"]`},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if cmc != 2.0 {
		t.Errorf("cmc = %v, want 2", cmc)
	}
}

// A transform card leaves the top-level type_line, mana_cost and oracle_text
// null and puts them in card_faces[]. Without the COALESCE these columns are
// null for exactly the cards people look up most, and a type filter silently
// omits them — the failure looks like "no results", not like a bug.
func TestRichCardDataReadsDoubleFacedCards(t *testing.T) {
	s := freshDB(t)

	const ajani = `{"id":"aj","name":"Ajani, Nacatl Pariah // Ajani, Nacatl Avenger",
	  "set":"mh3","collector_number":"237","rarity":"mythic","layout":"modal_dfc",
	  "cmc":2.0,"color_identity":["R","W"],
	  "card_faces":[
	    {"name":"Ajani, Nacatl Pariah","mana_cost":"{1}{W}",
	     "type_line":"Legendary Creature — Cat Warrior","oracle_text":"When Ajani enters..."},
	    {"name":"Ajani, Nacatl Avenger","mana_cost":"",
	     "type_line":"Legendary Planeswalker — Ajani","oracle_text":"+2: Put a +1/+1 counter..."}]}`

	if _, err := s.db.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name, scryfall_url, updated_at, raw_json)
VALUES ('aj','mh3','237','Ajani, Nacatl Pariah // Ajani, Nacatl Avenger','http://x','2026-07-30T00:00:00Z', ?)`,
		ajani); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var typeLine, mana, oracle string
	if err := s.db.QueryRow(`
SELECT type_line, mana_cost, oracle_text FROM cards WHERE scryfall_id = 'aj'`).
		Scan(&typeLine, &mana, &oracle); err != nil {
		t.Fatalf("reading front-face columns: %v", err)
	}
	if typeLine != "Legendary Creature — Cat Warrior" {
		t.Errorf("type_line = %q, want the front face's", typeLine)
	}
	if mana != "{1}{W}" {
		t.Errorf("mana_cost = %q, want the front face's", mana)
	}
	if !strings.HasPrefix(oracle, "When Ajani enters") {
		t.Errorf("oracle_text = %q, want the front face's", oracle)
	}

	// And it must be reachable by the filter that would otherwise miss it.
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM cards WHERE type_line LIKE '%Creature%'`).Scan(&n); err != nil {
		t.Fatalf("type filter: %v", err)
	}
	if n != 1 {
		t.Errorf("type filter matched %d rows, want the transform card", n)
	}
}

// Rows written before v5, or by an import that carries no Scryfall response,
// have no document to derive from. The columns must read NULL so callers can say
// "unknown" — a zero or an empty string would be indistinguishable from a real
// value and would quietly mislead.
func TestRichCardDataNullWithoutRawJSON(t *testing.T) {
	s := freshDB(t)
	if _, err := s.db.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name, scryfall_url, updated_at)
VALUES ('bare','uma','7','Ulamog','http://x','2026-07-30T00:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var rarity, typeLine sql.NullString
	var cmc sql.NullFloat64
	if err := s.db.QueryRow(
		`SELECT rarity, type_line, cmc FROM cards WHERE scryfall_id = 'bare'`).
		Scan(&rarity, &typeLine, &cmc); err != nil {
		t.Fatalf("reading columns: %v", err)
	}
	if rarity.Valid || typeLine.Valid || cmc.Valid {
		t.Errorf("want all NULL without raw_json, got %+v %+v %+v", rarity, typeLine, cmc)
	}
}

// Backups accumulate one per migration and are not small — raw_json took the
// newest from half a megabyte to nine — so old ones are pruned.
func TestBackupPruneKeepsTheRecentOnes(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "hoard.db")

	// Five old snapshots plus one hand-made backup that does not match the
	// pattern this function writes.
	var made []string
	for _, name := range []string{
		"hoard.db.bak-v1-20260101", "hoard.db.bak-v2-20260102",
		"hoard.db.bak-v3-20260103", "hoard.db.bak-v4-20260104",
		"hoard.db.bak-v5-20260105",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		made = append(made, p)
	}
	handmade := filepath.Join(dir, "hoard.db.backup-before-merge")
	if err := os.WriteFile(handmade, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A database that is not a backup at all must also survive.
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	current := filepath.Join(dir, "hoard.db.bak-v6-20260106")
	if err := os.WriteFile(current, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneBackups(db, current)

	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// The run's own backup is never a candidate, whatever the sort says.
	if !exists(current) {
		t.Error("the backup just written was removed")
	}
	// Two most recent of the older ones survive, making three in total.
	if !exists(made[4]) || !exists(made[3]) {
		t.Error("the most recent old backups were removed")
	}
	for _, p := range made[:3] {
		if exists(p) {
			t.Errorf("%s should have been pruned", filepath.Base(p))
		}
	}
	// Anything not matching the exact shape this writes is left alone.
	if !exists(handmade) {
		t.Error("a hand-made backup was swept up by the prune")
	}
	if !exists(db) {
		t.Fatal("the database itself was removed")
	}
}

// The version in a backup name is not zero-padded, so a lexical sort puts
// bak-v10 before bak-v9 and the prune would delete the newer snapshot while
// keeping the older — the one file the safety net exists for. Versions must
// compare as numbers.
func TestBackupPruneSortsVersionsNumerically(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "hoard.db")

	for _, name := range []string{
		"hoard.db.bak-v9-20260101",
		"hoard.db.bak-v10-20260102",
		"hoard.db.bak-v23-20260103",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(dir, "hoard.db.bak-v25-20260104")
	if err := os.WriteFile(current, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneBackups(db, current)

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if exists("hoard.db.bak-v9-20260101") {
		t.Error("v9 is the oldest and should have been pruned")
	}
	for _, name := range []string{
		"hoard.db.bak-v10-20260102", "hoard.db.bak-v23-20260103", "hoard.db.bak-v25-20260104",
	} {
		if !exists(name) {
			t.Errorf("%s should have survived the prune", name)
		}
	}
}

// Below the keep threshold nothing is touched.
func TestBackupPruneLeavesAFewAlone(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "hoard.db")
	for _, name := range []string{"hoard.db.bak-v1-20260101", "hoard.db.bak-v2-20260102"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(db, "")
	for _, name := range []string{"hoard.db.bak-v1-20260101", "hoard.db.bak-v2-20260102"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was pruned with only two present", name)
		}
	}
}

// The migration runner trusts the driver to execute every statement in a
// multi-statement migration; a driver that stopped early through a
// rename-recreate-copy rebuild (v23's shape) would leave card_entries empty
// and stamped. The runner's row-count check must catch a shrink and fail the
// open instead of proceeding on a gutted holdings table.
func TestMigrationRefusesShrunkCardEntries(t *testing.T) {
	savedMigrations, savedVersion := migrations, schemaVersion
	t.Cleanup(func() { migrations, schemaVersion = savedMigrations, savedVersion })
	// A stand-in for a rebuild whose copy step never ran.
	migrations = append(append([]migration(nil), migrations...),
		migration{savedVersion + 1, `DELETE FROM card_entries;`})
	schemaVersion = savedVersion + 1

	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',
                          10.0,25.0,'http://x','2020-01-01T00:00:00Z');
INSERT INTO containers (kind,name,source,source_id,created_at,updated_at)
  VALUES ('collection','Collection','manual','__collection__','x','x');
INSERT INTO card_entries VALUES (1,'ulamog-id','normal','main',2);`)

	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded across a migration that emptied card_entries")
	}
	if !strings.Contains(err.Error(), "card_entries") {
		t.Errorf("err = %v, want the shrunk-holdings refusal", err)
	}
}

// v19 gives the default binder its display name for real: a pre-v19 row named
// 'Collection' is renamed to 'Binder', so containers.name is authoritative and
// a rename is an ordinary UPDATE. The old stored name survives as an alias.
func TestMigrateRenamesDefaultBinder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO containers (kind,name,source,source_id,created_at,updated_at)
  VALUES ('collection','Collection','manual','__collection__','x','x');`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var name string
	if err := s.db.QueryRow(`SELECT name FROM containers WHERE source_id=?`,
		collectionSourceID).Scan(&name); err != nil {
		t.Fatalf("reading default binder name: %v", err)
	}
	if name != LooseName {
		t.Errorf("default binder name after migrating = %q, want %q", name, LooseName)
	}
	// The pre-v19 stored name still resolves, as a reserved alias.
	c, err := s.BinderByRef("Collection")
	if err != nil || !IsDefaultBinder(*c) {
		t.Errorf("BinderByRef(\"Collection\") = %v, %v; want the default", c, err)
	}
}
