package schemagen

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
)

func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

type object struct {
	kind string
	name string
	sql  string
}

func Generate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "hoard-schema")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "schema.db")
	s, err := store.Open(path)
	if err != nil {
		return nil, fmt.Errorf("migrating scratch database: %w", err)
	}
	if err := s.Close(); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	objs, err := readObjects(db)
	if err != nil {
		return nil, err
	}
	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		return nil, err
	}
	return render(objs, userVersion), nil
}

func readObjects(db *sql.DB) ([]object, error) {
	rows, err := db.Query(`
SELECT type, name, sql FROM sqlite_master
WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objs []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name, &o.sql); err != nil {
			return nil, err
		}
		objs = append(objs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].kind != objs[j].kind {
			return objs[i].kind == "table"
		}
		return objs[i].name < objs[j].name
	})
	return objs, nil
}

func render(objs []object, userVersion int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "-- hoard.db schema, version %d.\n", userVersion)

	for _, o := range objs {
		b.WriteString("\n")
		b.WriteString(format(o.sql))
		b.WriteString(";\n")
	}
	return []byte(b.String())
}

func format(stmt string) string {
	stmt = strings.TrimSpace(stmt)
	open := strings.Index(stmt, "(")
	close := strings.LastIndex(stmt, ")")
	if open < 0 || close < open {
		return collapse(stmt)
	}
	head := collapse(stmt[:open])
	body := stmt[open+1 : close]

	parts := splitTopLevel(body)

	if len(parts) < 2 {
		return collapse(stmt)
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(" (\n")
	for i, p := range parts {
		b.WriteString("    ")
		b.WriteString(collapse(p))
		if i < len(parts)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(")")
	return b.String()
}

func splitTopLevel(s string) []string {
	var parts []string
	depth, start := 0, 0
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])

	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
