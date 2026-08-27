package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const benchCardRaw = `{"object":"card","id":"sf-1","name":"Ragavan, Nimble Pilferer",
"set":"mh2","set_name":"Modern Horizons 2","collector_number":"138","rarity":"mythic",
"lang":"ja","layout":"normal","artist":"Simon Dominic","cmc":1.0,
"mana_cost":"{R}","color_identity":["R"],"type_line":"Legendary Creature — Monkey Pirate",
"oracle_text":"Dash {1}{R}","promo_types":["boosterfun","showcase"],
"image_uris":{"normal":"https://example.invalid/r.jpg"}}`

const fillerCards = 400

func buildV35(t *testing.T, path string) {
	t.Helper()
	ddl, err := os.ReadFile(filepath.Join("..", "..", "schema", "sqlite", "schema-v35.sql"))
	if err != nil {
		t.Fatalf("reading the published v35 schema: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatalf("applying v35 schema: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO containers (id, kind, name, source, source_id, created_at, updated_at)
VALUES (1, 'collection', 'Collection', 'hoard', 'collection', '2026-08-01', '2026-08-01');

INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, scryfall_url, updated_at, raw_json)
VALUES ('sf-1', 'mh2', '138', 'Ragavan, Nimble Pilferer',
        60.0, 'https://example.invalid/c', '2026-08-01', '` + benchCardRaw + `');

INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (1, 'sf-1', 'nonfoil', 'nm', 'main', 3);

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
VALUES ('sf-1', 'nonfoil', 59.5, 'tcgplayer', '2026-08-01T00:00:00Z');

PRAGMA user_version = 35;`); err != nil {
		t.Fatalf("seeding v35 data: %v", err)
	}

	filler := strings.Repeat("Whenever this creature attacks, scry two. ", 120)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, scryfall_url, updated_at, raw_json)
VALUES (?, 'fil', ?, ?, 1.0, 'https://example.invalid/f', '2026-08-01', ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := range fillerCards {
		id := fmt.Sprintf("fil-%04d", i)
		raw := fmt.Sprintf(`{"id":%q,"name":"Filler %d","lang":"en","set_name":"Filler Set",
"released_at":"2020-01-01","color_identity":["G"],"mana_cost":"{G}",
"promo_types":["x"],"oracle_text":%q}`, id, i, filler)
		if _, err := stmt.Exec(id, fmt.Sprint(i), fmt.Sprintf("Filler %d", i), raw); err != nil {
			t.Fatalf("seeding filler %d: %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestStoredColumnsMigrationKeepsEveryRowAndValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v35.db")
	buildV35(t, path)

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migrating v35 to latest): %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var ddl string
	if err := st.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='cards'`).Scan(&ddl); err != nil {
		t.Fatalf("reading the cards DDL: %v", err)
	}
	for _, col := range []string{"color_identity", "mana_cost", "promo_types", "lang",
		"set_name", "released_at"} {
		at := strings.Index(ddl, col+" ")
		if at < 0 {
			t.Fatalf("cards has no %s column:\n%s", col, ddl)
		}
		rest := ddl[at:]
		end := strings.Index(rest, "VIRTUAL")
		stored := strings.Index(rest, "STORED")
		if stored < 0 || (end >= 0 && end < stored) {
			t.Errorf("%s is still VIRTUAL, so every read re-parses raw_json", col)
		}
	}

	var ci, mana, promo, lang, name, set, setName, released string
	var price float64
	if err := st.db.QueryRow(`SELECT color_identity, mana_cost, promo_types, lang,
	                                 name, set_code, price_usd, set_name,
	                                 COALESCE(released_at, '') FROM cards
	                          WHERE scryfall_id = 'sf-1'`).
		Scan(&ci, &mana, &promo, &lang, &name, &set, &price, &setName, &released); err != nil {
		t.Fatalf("reading the migrated card: %v", err)
	}
	for _, c := range []struct{ got, want, what string }{
		{ci, `["R"]`, "color_identity"},
		{mana, "{R}", "mana_cost"},
		{promo, `["boosterfun","showcase"]`, "promo_types"},
		{lang, "ja", "lang"},
		{name, "Ragavan, Nimble Pilferer", "name"},
		{set, "mh2", "set_code"},
		{setName, "Modern Horizons 2", "set_name"},
		{released, "", "released_at"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q after migrating, want %q", c.what, c.got, c.want)
		}
	}
	if price != 60 {
		t.Errorf("price_usd = %v, want 60", price)
	}

	for _, c := range []struct {
		what string
		q    string
		want int
	}{
		{"cards", `SELECT COUNT(*) FROM cards`, 1 + fillerCards},
		{"card_entries", `SELECT COUNT(*) FROM card_entries`, 1},
		{"card_price_history", `SELECT COUNT(*) FROM card_price_history`, 1},
	} {
		var n int
		if err := st.db.QueryRow(c.q).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", c.what, err)
		}
		if n != c.want {
			t.Errorf("%s holds %d rows after migrating, want %d — the rebuild "+
				"lost data", c.what, n, c.want)
		}
	}

	rows, err := st.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tbl, parent string
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&tbl, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("scanning fk violation: %v", err)
		}
		t.Errorf("foreign key violation after the rebuild: %s -> %s", tbl, parent)
	}

	var free int
	if err := st.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatalf("reading freelist: %v", err)
	}
	if free > 16 {
		t.Errorf("%d free pages left after the rebuild; the file keeps the space "+
			"the old table occupied", free)
	}

	for _, idx := range []string{"cards_name", "cards_mtgjson_uuid",
		"cards_trait_filter", "cards_set_code"} {
		var n int
		if err := st.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`,
			idx).Scan(&n); err != nil {
			t.Fatalf("looking for %s: %v", idx, err)
		}
		if n != 1 {
			t.Errorf("index %s did not survive the table rebuild", idx)
		}
	}
}
