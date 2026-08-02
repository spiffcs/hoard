package browse

// The interop prompts: importing and exporting cross the program boundary
// (a URL, a file on disk), so each collects its input through the ordinary
// prompt machinery and hands the real work to an injected closure — the
// network and the filesystem stay outside browse.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

// expandPath resolves a leading ~/ against the home directory; everything
// else passes through untouched.
func expandPath(s string) string {
	if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, s[2:])
		}
	}
	return s
}

// promptDeckURL asks for a deck link and runs the import as an operation.
// Validation stays light — scheme and host — because decksource.Fetch's
// provider errors (including the Moxfield-blocked message) are better than
// anything a prompt could pre-empt, and they surface through the op's
// error path.
func (m *Model) promptDeckURL() {
	m.prompt = &prompt{
		label: "deck URL",
		validate: func(text string) error {
			u, err := url.Parse(strings.TrimSpace(text))
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("paste an http(s) deck link")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd {
			return m.startDeckAdd(strings.TrimSpace(text))
		},
	}
}

// startDeckAdd runs the injected deck import: fetch and resolve both happen
// inside the op, off the UI thread.
func (m *Model) startDeckAdd(deckURL string) tea.Cmd {
	fn := m.opDeckAdd
	return m.startOpReport("importing deck", func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		r, err := fn(ctx, p, deckURL)
		if err != nil {
			return opOutcome{}, err
		}
		return opOutcome{summary: r.Summary, report: r.Report}, nil
	})
}

// promptCardURL asks for a Scryfall card link and adds it as an operation.
func (m *Model) promptCardURL() {
	m.prompt = &prompt{
		label: "Scryfall card URL",
		validate: func(text string) error {
			u, err := url.Parse(strings.TrimSpace(text))
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("paste an http(s) Scryfall card link")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd {
			fn := m.opAddURL
			cardURL := strings.TrimSpace(text)
			return m.startOp("adding card", func(ctx context.Context, p progress.Fn) (string, error) {
				return fn(ctx, p, cardURL)
			})
		},
	}
}

// promptImportPath asks for a collection file and runs the import as an
// operation. The prompt validates existence; format sniffing and the ledger
// check live in the injected closure.
func (m *Model) promptImportPath() {
	m.prompt = &prompt{
		label: "import which file (CSV)",
		validate: func(text string) error {
			p := expandPath(strings.TrimSpace(text))
			if p == "" {
				return fmt.Errorf("name a file")
			}
			fi, err := os.Stat(p)
			if err != nil {
				return fmt.Errorf("no such file")
			}
			if fi.IsDir() {
				return fmt.Errorf("that is a directory")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd {
			return m.startImport(expandPath(strings.TrimSpace(text)), false)
		},
	}
}

// exportFormats is what the format prompt accepts.
var exportFormats = map[string]bool{"csv": true, "json": true, "moxfield": true, "archidekt": true}

// formatFromExt prefills the format prompt from the path's extension: a
// .json path almost certainly wants JSON, everything else defaults to the
// canonical CSV.
func formatFromExt(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return "json"
	}
	return "csv"
}

// slugify turns a container name into a filename fragment.
func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "hoard-export"
	}
	return "hoard-" + s
}

// promptExport chains path → format, then an overwrite confirm when the
// file already exists — safer than the CLI's silent truncate, and cheap
// with the machinery already here.
func (m *Model) promptExport(binderRef, deckRef, slug string) {
	m.prompt = &prompt{
		label: "export to (file path)",
		text:  "~/" + slug + ".csv",
		validate: func(text string) error {
			p := expandPath(strings.TrimSpace(text))
			if p == "" {
				return fmt.Errorf("name a file")
			}
			if fi, err := os.Stat(filepath.Dir(p)); err != nil || !fi.IsDir() {
				return fmt.Errorf("no such directory")
			}
			return nil
		},
		commit: func(m *Model, text string) tea.Cmd {
			path := expandPath(strings.TrimSpace(text))
			m.prompt = &prompt{
				label: "format (csv/json/moxfield/archidekt)",
				text:  formatFromExt(path),
				validate: func(ftext string) error {
					if !exportFormats[strings.TrimSpace(strings.ToLower(ftext))] {
						return fmt.Errorf("csv, json, moxfield, or archidekt")
					}
					return nil
				},
				commit: func(m *Model, ftext string) tea.Cmd {
					format := strings.TrimSpace(strings.ToLower(ftext))
					if _, err := os.Stat(path); err == nil {
						m.confirm = &pendingConfirm{
							prompt: filepath.Base(path) + " exists — overwrite?",
							help:   "y overwrite · any other key cancels",
							onYes: func(m *Model) tea.Cmd {
								m.runExport(binderRef, deckRef, format, path)
								return nil
							},
						}
						return nil
					}
					m.runExport(binderRef, deckRef, format, path)
					return nil
				},
			}
			return nil
		},
	}
}

// runExport calls the injected writer and reports on the status line.
func (m *Model) runExport(binderRef, deckRef, format, path string) {
	summary, err := m.exportFn(binderRef, deckRef, format, path)
	if err != nil {
		m.status, m.statusErr = err.Error(), true
		return
	}
	m.status, m.statusErr = summary, false
}

// startImport runs the injected import. The ledger's run-again question is
// built here — the one place path is still in scope — and rides the done
// message as data; answering yes re-runs the same import with the doubling
// acknowledged.
func (m *Model) startImport(path string, again bool) tea.Cmd {
	fn := m.opImport
	title := "importing " + filepath.Base(path)
	return m.startOpReport(title, func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		r, err := fn(ctx, p, path, again)
		if err != nil {
			return opOutcome{}, err
		}
		oc := opOutcome{summary: r.Summary, report: r.Report}
		if r.AlreadyImported != "" && !again {
			oc.summary = "import refused — already imported"
			oc.confirm = &pendingConfirm{
				prompt: r.AlreadyImported + " — import it again?",
				help:   "y import again · any other key cancels",
				onYes:  func(m *Model) tea.Cmd { return m.startImport(path, true) },
			}
		}
		return oc, nil
	})
}
