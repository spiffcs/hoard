package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/ui"
)

func readPathOrStdin(path string) (data []byte, display string, err error) {
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
		return data, "stdin", err
	}
	data, err = os.ReadFile(path)
	return data, path, err
}

func catalogDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "catalog")
}

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

		ui.NewReport().Progress(
			"The local catalog predates this hoard's format; the next update rebuilds it in full.")
	}
	return c
}

func stderrPrinter() *ui.Printer {
	return ui.NewPrinter(os.Stderr, isTTY(os.Stderr))
}

func confirm(question string) bool {
	if !stdinIsTTY() {
		return false
	}
	ok, err := ui.Confirm(os.Stdin, os.Stderr, question)
	return err == nil && ok
}

func confirmOnTerminal(question string) bool {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	defer tty.Close()
	ok, err := ui.Confirm(tty, os.Stderr, question)
	return err == nil && ok
}

func isTTY(f *os.File) bool { return ui.IsTerminal(f) }

func stdinIsTTY() bool { return isTTY(os.Stdin) }
