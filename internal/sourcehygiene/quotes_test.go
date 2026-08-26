package sourcehygiene

import (
	"strconv"
	"strings"
	"testing"
)

func TestGoSourcesHaveNoUnpairedTypographicQuote(t *testing.T) {
	const (
		openQuote  = '“'
		closeQuote = '”'
	)

	var problems []string
	for _, src := range moduleSources(t, ".go") {
		opens := strings.Count(src.Text, string(openQuote))
		closes := strings.Count(src.Text, string(closeQuote))
		if opens == closes {
			continue
		}
		problems = append(problems, src.Rel+": "+strconv.Itoa(opens)+" opening, "+
			strconv.Itoa(closes)+" closing")
		for n, line := range strings.Split(src.Text, "\n") {
			if strings.ContainsRune(line, openQuote) || strings.ContainsRune(line, closeQuote) {
				problems = append(problems, "    "+strconv.Itoa(n+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(problems) > 0 {
		t.Errorf("unpaired typographic quote — a doc comment naming a pair of "+
			"straight single quotes or backquotes was rewritten by gofmt; say it "+
			"in words instead:\n%s", strings.Join(problems, "\n"))
	}
}
