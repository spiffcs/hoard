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

// A typographic double quote must not appear alone in a Go source: the opening
// U+201C and the closing U+201D come in pairs, and a file holding more of one
// than the other has a corrupted comment in it.
//
// GOFMT IS THE CAUSE, AND THAT IS THE WHOLE POINT OF THIS TEST. In a DOC
// comment — one attached to a declaration — gofmt applies the old godoc
// convention for typographic quotes:
//
//	a pair of straight single quotes  ->  U+201D, a closing curly quote
//	a pair of backquotes              ->  U+201C, an opening curly quote
//
// A pair of straight DOUBLE quotes is left alone, and so is everything in a
// non-doc comment. That is a deterministic rewrite by a formatter the repo runs
// as a gate, not a slip anybody can be careful about — write the SQL empty
// string as a pair of straight single quotes in a doc comment and gofmt will
// silently turn it into one curly character on the next run.
//
// It has bitten twice, both times in comments about EMPTY strings, which is
// what makes it so quiet: the thing being named was a PAIR of quotes with
// nothing between them, and it came back as a single character that looks like
// it might still be that pair.
//
//   - migrate.go argued the unknown-condition sentinel is deliberately not the
//     SQL empty string, beside the neighbouring 'nm'. It came back ruling out a
//     character that is not a value at all.
//   - holdings.go named the Go zero value as not being in the condition
//     vocabulary. Same rewrite, so it named nothing.
//
// WHY UNPAIRED RATHER THAN A FLAT BAN. Banning the characters outright would be
// simpler and it is what this test did first, but it is wrong: the convention
// above is a documented gofmt feature, so a doc comment that legitimately
// quotes something produces a MATCHED pair and a flat ban would fail on code
// that is correct and gofmt-clean. A check that fights the formatter gets
// suppressed, and then it protects nothing.
//
// What is always wrong is a lone one. Legitimate use opens and closes; the
// defect is a single closing quote standing where a literal was meant. Counting
// per file rather than per comment keeps it free of judgement about which
// comment a quote belongs to, and costs nothing in precision: a quotation that
// opens and closes anywhere in the file balances.
//
// The characters are built from their code points below so this file does not
// trip its own check.
//
// U+2018 and U+2019, the curly SINGLE quotes, are not checked at all. U+2019 is
// an ordinary apostrophe in English prose, it is inherently unpaired, and the
// pair-of-quotes confusion cannot arise for it.
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

// repoRoot is this file's directory two levels up: internal/sourcehygiene to
// the module root. Taken from the compiled-in path rather than the working
// directory, which `go test` sets to the package under test — so the walk
// covers the repo wherever it is checked out.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this file, so the repo root is unknown")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	// Assert the root rather than trusting the arithmetic: a moved package
	// would otherwise walk some parent directory and quietly pass.
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s, so this would walk the wrong tree: %v", root, err)
	}
	return root
}
