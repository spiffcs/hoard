package schemagen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func schemaPath(name string) string {
	return filepath.Join(RepoRoot(), "schema", "sqlite", name)
}

// TestSchemaFilesMatchMigrations is the drift check: the committed SQL must be
// exactly what migrating an empty database produces. When it fails, run
// `make generate-sqlite-schema` — and if schema-v<version>.sql was already
// released, add a migration and let it write a new file rather than editing
// the old one: released files are immutable (see schema/sqlite/README.md).
func TestSchemaFilesMatchMigrations(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	names := []string{
		"schema-latest.sql",
		fmt.Sprintf("schema-v%d.sql", store.SchemaVersion()),
	}
	for _, name := range names {
		onDisk, err := os.ReadFile(schemaPath(name))
		if err != nil {
			t.Fatalf("reading %s: %v (run `make generate-sqlite-schema`)", name, err)
		}
		if !bytes.Equal(onDisk, generated) {
			t.Errorf("%s does not match the migrations in internal/store/migrate.go.\n"+
				"Run `make generate-sqlite-schema`; if this schema version was already "+
				"released, add a migration instead of editing the released file.", name)
		}
	}
}

// TestDumpNamesTheCurrentVersion guards the header against the version drifting
// out of the file it describes — a dump that claims the wrong version is worse
// than no dump, since the whole point is telling a reader which schema they hold.
func TestDumpNamesTheCurrentVersion(t *testing.T) {
	out, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := fmt.Sprintf("-- hoard.db schema, version %d.", store.SchemaVersion())
	if !strings.HasPrefix(string(out), want) {
		t.Errorf("dump does not open with %q", want)
	}
}

// TestDumpCoversEveryTable pins the published surface. A migration that adds a
// table without the dump growing means the generator stopped seeing part of the
// schema — the one failure that would leave consumers reading an incomplete
// contract while the byte-comparison above still passed.
func TestDumpCoversEveryTable(t *testing.T) {
	out, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, table := range []string{
		"cards", "containers", "card_entries", "card_prices_alt",
		"card_price_history", "card_bid_history", "card_price_gaps",
		"import_ledger", "settings", "value_snapshots", "watches",
	} {
		if !strings.Contains(string(out), "CREATE TABLE "+table+" (") {
			t.Errorf("dump is missing CREATE TABLE %s", table)
		}
	}
	// sqlite_sequence is SQLite's own bookkeeping for AUTOINCREMENT; publishing
	// it would invite a reader to treat it as part of hoard's model.
	if strings.Contains(string(out), "sqlite_sequence") {
		t.Error("dump includes sqlite_sequence, which is SQLite's bookkeeping, not hoard's schema")
	}
}

// TestSplitTopLevelKeepsNestedCommas covers the case the formatter exists for:
// a generated column whose json_extract arguments contain the very character
// the split runs on.
func TestSplitTopLevelKeepsNestedCommas(t *testing.T) {
	got := splitTopLevel(
		`a TEXT, b TEXT GENERATED ALWAYS AS (COALESCE(json_extract(r,'$.x'), json_extract(r,'$.y'))) VIRTUAL, c INTEGER`)
	want := []string{
		`a TEXT`,
		`b TEXT GENERATED ALWAYS AS (COALESCE(json_extract(r,'$.x'), json_extract(r,'$.y'))) VIRTUAL`,
		`c INTEGER`,
	}
	if len(got) != len(want) {
		t.Fatalf("split into %d parts, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSplitTopLevelIgnoresQuotedCommas covers a DEFAULT whose literal contains
// a comma, which would otherwise split a column definition in half.
func TestSplitTopLevelIgnoresQuotedCommas(t *testing.T) {
	got := splitTopLevel(`a TEXT NOT NULL DEFAULT 'x,y', b TEXT`)
	if len(got) != 2 || got[0] != `a TEXT NOT NULL DEFAULT 'x,y'` || got[1] != "b TEXT" {
		t.Errorf("split = %q, want two parts with the quoted comma intact", got)
	}
}
