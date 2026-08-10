package command

// Helpers shared by more than one command: where the re-downloadable caches
// live, and the two I/O affordances that talk to the real terminal rather than
// to a command's Env — progress narration and the yes/no prompt.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/ui"
)

// readPathOrStdin reads a file, or stdin when the path is a lone dash, and
// returns the bytes alongside the name to call that source.
//
// One copy, because the dash is a promise about the CLI rather than about any
// one command: add --file, import and watch import all take a file the user
// might just as easily have in a pipe, and three hand-rolled copies of eight
// lines are three chances for one of them to drift. The display name comes
// back with the bytes because every one of those surfaces names its source
// back to the user — in a receipt, or in the sentence that says the file would
// not parse — and a dash in that sentence reads like a flag.
//
// Not used by deck add: its dash needs a name for the deck first, so it is
// handled where the flags are (see readDeckFromStdin).
func readPathOrStdin(path string) (data []byte, display string, err error) {
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
		return data, "stdin", err
	}
	data, err = os.ReadFile(path)
	return data, path, err
}

// catalogDir is where the local card catalog lives.
//
// The cache directory, beside the MTGJSON bundles and deliberately nowhere near
// hoard.db. Every byte of it is re-downloadable and a collection is not, so
// losing this to eviction costs a rebuild while losing the other costs
// everything. It is also what keeps the migration runner's VACUUM INTO backup
// from copying sixty megabytes of card data on every schema change.
func catalogDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "catalog")
}

// openCatalog opens the local catalog, or returns nil if it cannot be opened.
//
// A nil catalog is a supported state, not an error: every caller falls through
// to the Scryfall API, so a machine with no writable cache directory simply
// behaves the way hoard did before the catalog existed.
func openCatalog() *catalog.Catalog {
	dir := catalogDir()
	if dir == "" {
		return nil
	}
	c, err := catalog.Open(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog unavailable, using the Scryfall API: %v\n", err)
		return nil
	}
	if c.ReplacedOutdated() {
		// The schema bump wiped a populated catalog; without this line the
		// next download prompt reads as "your catalog vanished".
		ui.NewReport().Progress(
			"The local catalog predates this hoard's format; the next update rebuilds it in full.")
	}
	return c
}

// stderrPrinter is the CLI's progress renderer: narration belongs on stderr
// (stdout is the data stream), updating in place only when stderr really is
// a terminal.
func stderrPrinter() *ui.Printer {
	return ui.NewPrinter(os.Stderr, isTTY(os.Stderr))
}

// confirm asks a yes/no question, defaulting to no.
//
// A non-interactive stdin declines outright rather than blocking a script
// forever on a prompt nobody will answer. The prompt itself goes to stderr —
// it is conversation with the user, not command output, and it must not
// leak into a pipe that happens to still have a terminal on stdin. The ask
// itself is ui.Confirm, the same [y/N] every confirm in hoard speaks.
func confirm(question string) bool {
	if !stdinIsTTY() {
		return false
	}
	ok, err := ui.Confirm(os.Stdin, os.Stderr, question)
	return err == nil && ok
}

// isTTY reports whether a file is an interactive terminal, answered the
// same way the renderer answers it (ui.Detect) rather than by a second
// hand-rolled Stat probe.
func isTTY(f *os.File) bool { return ui.IsTerminal(f) }

// stdinIsTTY reports whether stdin is interactive, which the TUI requires.
func stdinIsTTY() bool { return isTTY(os.Stdin) }
