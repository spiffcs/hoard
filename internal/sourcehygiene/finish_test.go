package sourcehygiene

import (
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

	var problems []string
	for _, src := range moduleSources(t, ".go", ".sql") {
		rel := src.Rel
		if vocabularyTests[rel] || wireFixtures[rel] {
			continue
		}
		if strings.HasPrefix(rel, "schema/sqlite/schema-v") {
			continue
		}
		for n, line := range strings.Split(src.Text, "\n") {
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
	}
	if len(problems) > 0 {
		t.Errorf("MTGJSON spells nonfoil \"normal\"; hoard does not. The only place that word "+
			"belongs is the json tag in %s that decodes the payload — everything downstream "+
			"must already see nonfoil:\n%s", decodePoint, strings.Join(problems, "\n"))
	}
}
