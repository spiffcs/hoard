package sourcehygiene

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestGoSourcesHaveNoUnpairedTypographicQuote(t *testing.T) {
	const (
		openQuote  = '“'
		closeQuote = '”'
	)

	root := repoRoot(t)
	var problems []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(src)
		opens := strings.Count(text, string(openQuote))
		closes := strings.Count(text, string(closeQuote))
		if opens == closes {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		problems = append(problems, rel+": "+strconv.Itoa(opens)+" opening, "+
			strconv.Itoa(closes)+" closing")
		for n, line := range strings.Split(text, "\n") {
			if strings.ContainsRune(line, openQuote) || strings.ContainsRune(line, closeQuote) {
				problems = append(problems, "    "+strconv.Itoa(n+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(problems) > 0 {
		t.Errorf("unpaired typographic quote — a doc comment naming a pair of "+
			"straight single quotes or backquotes was rewritten by gofmt; say it "+
			"in words instead:\n%s", strings.Join(problems, "\n"))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this file, so the repo root is unknown")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s, so this would walk the wrong tree: %v", root, err)
	}
	return root
}
