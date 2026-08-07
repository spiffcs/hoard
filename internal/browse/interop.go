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
//
// The help spends two rows because the second is the way out: the reader who
// pasted a Moxfield link needs a next step, and AddDeckFromFile is one esc
// away rather than a trip back to the shell.
func (m *Model) promptDeckURL() {
	m.prompt = &prompt{
		label: "deck URL",
		help: "archidekt links work, like https://archidekt.com/decks/123456\n" +
			"Moxfield blocks fetching. Export the deck, then use AddDeckFromFile\n" +
			"enter accept · esc cancel",
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

// promptDeckFile asks for an exported decklist and runs the import as an
// operation. This is the other half of deck import: the providers that block
// fetching still export, and a file is the only thing they hand over.
//
// The deck's name and provider are the closure's business — it takes them
// from the file — so the prompt stays one answer, like every other.
func (m *Model) promptDeckFile() {
	m.prompt = &prompt{
		label: "deck file",
		help:  "a decklist you exported, one card per line · enter accept · esc cancel",
		validate: func(text string) error {
			return existingFile(expandPath(strings.TrimSpace(text)))
		},
		commit: func(m *Model, text string) tea.Cmd {
			return m.startDeckAddFile(expandPath(strings.TrimSpace(text)))
		},
	}
}

// startDeckAddFile runs the injected file import: parsing and resolving both
// happen inside the op, off the UI thread.
func (m *Model) startDeckAddFile(path string) tea.Cmd {
	fn := m.opDeckAddFile
	return m.startOpReport("importing deck", func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		r, err := fn(ctx, p, path)
		if err != nil {
			return opOutcome{}, err
		}
		return opOutcome{summary: r.Summary, report: r.Report}, nil
	})
}

// existingFile is the validation every file prompt shares: named, present,
// and not a directory. The prompts differ in what they do with the path, not
// in what makes one answerable.
func existingFile(path string) error {
	if path == "" {
		return fmt.Errorf("name a file")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no such file")
	}
	if fi.IsDir() {
		return fmt.Errorf("that is a directory")
	}
	return nil
}

// promptImportPath asks for a collection file and runs the import as an
// operation. The prompt validates existence; format sniffing and the ledger
// check live in the injected closure.
func (m *Model) promptImportPath() {
	m.prompt = &prompt{
		label: "import which file (CSV)",
		validate: func(text string) error {
			return existingFile(expandPath(strings.TrimSpace(text)))
		},
		commit: func(m *Model, text string) tea.Cmd {
			return m.startImport(expandPath(strings.TrimSpace(text)), false)
		},
	}
}

// promptWatchImportPath asks for a watch-list file and runs the import as an
// operation. The prompt validates existence; format sniffing lives in the
// injected closure. No ledger, no again: watch import upserts, so re-running
// a file is how thresholds move.
func (m *Model) promptWatchImportPath() {
	m.prompt = &prompt{
		label: "import watches from (CSV or JSON)",
		validate: func(text string) error {
			return existingFile(expandPath(strings.TrimSpace(text)))
		},
		commit: func(m *Model, text string) tea.Cmd {
			return m.startWatchImport(expandPath(strings.TrimSpace(text)))
		},
	}
}

// startWatchImport runs the injected watch import: reading and resolving
// both happen inside the op, off the UI thread.
func (m *Model) startWatchImport(path string) tea.Cmd {
	fn := m.opWatchImport
	return m.startOpReport("importing watches", func(ctx context.Context, p progress.Fn) (opOutcome, error) {
		r, err := fn(ctx, p, path)
		if err != nil {
			return opOutcome{}, err
		}
		return opOutcome{summary: r.Summary, report: r.Report}, nil
	})
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
							prompt: filepath.Base(path) + " exists. Overwrite?",
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
			oc.summary = "import refused: already imported"
			oc.confirm = &pendingConfirm{
				prompt: r.AlreadyImported + ". Import it again?",
				help:   "y import again · any other key cancels",
				onYes:  func(m *Model) tea.Cmd { return m.startImport(path, true) },
			}
		}
		return oc, nil
	})
}
