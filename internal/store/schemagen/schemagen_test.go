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

	if strings.Contains(string(out), "sqlite_sequence") {
		t.Error("dump includes sqlite_sequence, which is SQLite's bookkeeping, not hoard's schema")
	}
}

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

func TestSplitTopLevelIgnoresQuotedCommas(t *testing.T) {
	got := splitTopLevel(`a TEXT NOT NULL DEFAULT 'x,y', b TEXT`)
	if len(got) != 2 || got[0] != `a TEXT NOT NULL DEFAULT 'x,y'` || got[1] != "b TEXT" {
		t.Errorf("split = %q, want two parts with the quoted comma intact", got)
	}
}
