package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

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

func freshDB(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

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
    finish       TEXT NOT NULL,
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);
PRAGMA user_version = 0;`

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

	if _, ok := ids["no-id"]; ok {
		t.Errorf("ids = %v, want no entry for a card that never had one", ids)
	}

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

func TestMigrationBacksUpExistingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hoard.db")

	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',
                          10.0,25.0,'http://x','2020-01-01T00:00:00Z');
INSERT INTO containers (kind,name,source,source_id,created_at,updated_at)
  VALUES ('collection','Collection','manual','__collection__','x','x');
INSERT INTO card_entries VALUES (1,'ulamog-id','nonfoil','main',2);`)

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

	if held := heldByFinish(t, s, "ulamog-id"); held["nonfoil"] != 2 {
		t.Errorf("after migrating: %v; want the 2 copies intact", held)
	}
}

func TestSplitAltSourceBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")

	seedRawDB(t, path, schemaV1+`
INSERT INTO cards VALUES ('ripple-id','m3c','218','Acidic Slime',
                          0.34,NULL,'http://x','x');
INSERT INTO cards VALUES ('sol-id','c21','1','Sol Ring',2.0,5.0,'http://x','x');
-- Two shops, one per finish, written the way v1 wrote them.
INSERT INTO card_prices_alt VALUES ('ripple-id','u1',0.34,0.49,'tcgplayer/cardkingdom','x');
-- One shop for both.
INSERT INTO card_prices_alt VALUES ('sol-id','u2',1.00,2.00,'manapool','x');
PRAGMA user_version = 1;`)

	s, err := Open(path)
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

	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM cards WHERE type_line LIKE '%Creature%'`).Scan(&n); err != nil {
		t.Fatalf("type filter: %v", err)
	}
	if n != 1 {
		t.Errorf("type filter matched %d rows, want the transform card", n)
	}
}

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

func TestBackupPruneKeepsTheRecentOnes(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "hoard.db")

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

	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	current := filepath.Join(dir, "hoard.db.bak-v6-20260106")
	if err := os.WriteFile(current, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneBackups(db, current)

	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	if !exists(current) {
		t.Error("the backup just written was removed")
	}

	if !exists(made[4]) || !exists(made[3]) {
		t.Error("the most recent old backups were removed")
	}
	for _, p := range made[:3] {
		if exists(p) {
			t.Errorf("%s should have been pruned", filepath.Base(p))
		}
	}

	if !exists(handmade) {
		t.Error("a hand-made backup was swept up by the prune")
	}
	if !exists(db) {
		t.Fatal("the database itself was removed")
	}
}

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

func TestMigrationRefusesShrunkCardEntries(t *testing.T) {
	savedMigrations, savedVersion := migrations, schemaVersion
	t.Cleanup(func() { migrations, schemaVersion = savedMigrations, savedVersion })

	migrations = append(append([]migration(nil), migrations...),
		migration{savedVersion + 1, `DELETE FROM card_entries;`})
	schemaVersion = savedVersion + 1

	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',
                          10.0,25.0,'http://x','2020-01-01T00:00:00Z');
INSERT INTO containers (kind,name,source,source_id,created_at,updated_at)
  VALUES ('collection','Collection','manual','__collection__','x','x');
INSERT INTO card_entries VALUES (1,'ulamog-id','nonfoil','main',2);`)

	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded across a migration that emptied card_entries")
	}
	if !strings.Contains(err.Error(), "card_entries") {
		t.Errorf("err = %v, want the shrunk-holdings refusal", err)
	}
}

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

	c, err := s.BinderByRef("Collection")
	if err != nil || !IsDefaultBinder(*c) {
		t.Errorf("BinderByRef(\"Collection\") = %v, %v; want the default", c, err)
	}
}
