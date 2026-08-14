package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/hoardjson/schemagen"
)

func main() {
	out, err := schemagen.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	dir := filepath.Join(schemagen.RepoRoot(), "schema", "json")
	versioned := filepath.Join(dir, "schema-"+hoardjson.SchemaVersion+".json")
	for _, path := range []string{versioned, filepath.Join(dir, "schema-latest.json")} {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}
}
