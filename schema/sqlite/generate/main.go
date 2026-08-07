// Command generate writes the current hoard.db schema to
// schema/sqlite/schema-v<version>.sql and schema-latest.sql.
//
// Run it via `make generate-sqlite-schema` after adding a migration to
// internal/store/migrate.go. Released schema files are immutable, so a new
// migration means a new file rather than an edit to an old one — see
// schema/sqlite/README.md.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/store/schemagen"
)

func main() {
	out, err := schemagen.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	dir := filepath.Join(schemagen.RepoRoot(), "schema", "sqlite")
	versioned := filepath.Join(dir, fmt.Sprintf("schema-v%d.sql", store.SchemaVersion()))
	for _, path := range []string{versioned, filepath.Join(dir, "schema-latest.sql")} {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}
}
