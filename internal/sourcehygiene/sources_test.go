package sourcehygiene

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type source struct {
	Rel  string
	Text string
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this file, so the module root is unknown")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s, so this would read the wrong tree: %v", root, err)
	}
	return root
}

func ownsDir(root, path string, d fs.DirEntry) bool {
	if path == root {
		return true
	}
	switch d.Name() {
	case "vendor", "node_modules", "dist", "bin":
		return false
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err != nil
}

func moduleSources(t *testing.T, exts ...string) []source {
	t.Helper()
	root := moduleRoot(t)
	want := make(map[string]bool, len(exts))
	for _, e := range exts {
		want[e] = true
	}

	var out []source
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !ownsDir(root, path, d) {
				return filepath.SkipDir
			}
			return nil
		}
		if !want[filepath.Ext(path)] {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, source{Rel: filepath.ToSlash(rel), Text: string(text)})
		return nil
	})
	if err != nil {
		t.Fatalf("reading the module's sources: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("found no %s files under %s", strings.Join(exts, "/"), root)
	}
	return out
}
