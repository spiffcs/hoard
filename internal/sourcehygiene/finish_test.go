package sourcehygiene

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNormalIsSpokenOnlyWhereMTGJSONIsDecoded(t *testing.T) {
	decodePoint := "internal/mtgjson/mtgjson.go"
	wireFixtures := map[string]bool{
		"internal/mtgjson/mtgjson_test.go": true,
	}
	vocabularyTests := map[string]bool{
		"internal/mtgjson/finish_test.go":       true,
		"internal/store/finish_test.go":         true,
		"internal/finish/finish_test.go":        true,
		"internal/store/finish_schema_test.go":  true,
		"internal/sourcehygiene/finish_test.go": true,
	}

	root := repoRoot(t)
	var problems []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".sql":
		default:
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if vocabularyTests[rel] || wireFixtures[rel] {
			return nil
		}
		if strings.HasPrefix(rel, "schema/sqlite/schema-v") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for n, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "image_uris") || strings.Contains(line, "ayout") {
				continue
			}
			if strings.Contains(line, `"normal":`) {
				continue
			}
			if rel == decodePoint && strings.Contains(line, `json:"normal"`) {
				continue
			}
			if strings.Contains(line, `'normal'`) || strings.Contains(line, `"normal"`) {
				problems = append(problems, rel+":"+strconv.Itoa(n+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(problems) > 0 {
		t.Errorf("MTGJSON spells nonfoil \"normal\"; hoard does not. The only place that word "+
			"belongs is the json tag in %s that decodes the payload — everything downstream "+
			"must already see nonfoil:\n%s", decodePoint, strings.Join(problems, "\n"))
	}
}
