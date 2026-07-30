// Command hoard catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/browse"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const usage = `hoard — catalog valuable MTG cards and decks in SQLite

Usage:
  hoard [--db PATH] [command] [args]

  hoard                                            Browse the hoard (no arguments)

Browsing replaces the old list, summary, deck list and deck show commands: it
shows your binder and every deck beside their cards, filters by name or card trait, edits
quantities in place, and reaches movers, unpriced and arbitrage with 'v'. Piped
or redirected, plain 'hoard' prints the summary table instead, so 'hoard | grep'
still works.

Collection commands:
  add                                              Add cards interactively by name
  update-prices [--limit N]                        Refresh prices (Scryfall updates daily)
  movers [--since 30d] [--limit N]                 Biggest risers and sinkers you hold
  backfill-prices                                  Load 90 days of past prices from MTGJSON
  unpriced                                         Cards counting as $0.00, and why
  repair-finishes                                  Fix cards stored as a finish they lack
  arbitrage [--min N] [--limit N]                  Where vendors disagree, as three tables
  catalog [status|update]                          The local copy of Scryfall's card data

Binder commands:
  binder list                                      Your binders, with counts and value
  binder new <name>                                Create a named binder
  binder rename <binder> <new-name>                Rename a binder
  binder rm <binder>                               Remove an empty binder

Deck commands:
  deck add <archidekt-url>                         Import/refresh a deck from a link
  deck add --file <path> [--name NAME] [--source S]  Import a text/exported decklist
  deck remove <name>                               Delete a deck

Interop commands:
  export [--binder B | --deck D | --all] [-o FILE] Holdings as CSV (everything by default)
         [--format csv|moxfield|archidekt]           in hoard's format or theirs
  import FILE [--binder B | --preserve-binders]    Add a collection CSV export (ManaBox,
         [--format F] [--dry-run]                    Moxfield, Delver Lens, hoard)

A deck <name> can be any part of its name, as long as it matches one deck.

The database lives in a per-user data directory by default (e.g. on macOS
~/Library/Application Support/hoard/hoard.db, on Linux $XDG_DATA_HOME/hoard/hoard.db)
— so it's the same hoard from any directory. Override with --db or $HOARD_DB.
Moxfield's API is Cloudflare-blocked; export that deck to text and use 'deck add --file'.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	rest, dbFlag, err := extractDBFlag(args)
	if err != nil {
		fmt.Fprint(os.Stderr, usage)
		return err
	}

	// No command opens the browser. It replaces what used to be four separate
	// read commands — list, summary, deck list, deck show — so the thing you
	// reach for most often is also the thing you get by typing nothing.
	cmd := ""
	var cmdArgs []string
	if len(rest) > 0 {
		cmd, cmdArgs = rest[0], rest[1:]
	}

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return nil
	}

	// --db wins over $HOARD_DB, which in turn wins over the per-user default.
	dbPath := dbFlag
	if dbPath == "" {
		if dbPath, err = defaultDBPath(); err != nil {
			return err
		}
	}

	// Note whether we're about to create the database, so we can tell the user
	// where it lives the first time.
	_, statErr := os.Stat(dbPath)
	newDB := os.IsNotExist(statErr)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if newDB {
		fmt.Fprintf(os.Stderr, "Initialized hoard database at %s\n", dbPath)
	}

	ctx := context.Background()
	switch cmd {
	case "":
		return cmdBrowse(ctx, st)
	case "add":
		return cmdAdd(ctx, st, cmdArgs)
	case "update-prices":
		return cmdUpdatePrices(ctx, st, cmdArgs)
	case "movers":
		return cmdMovers(st, cmdArgs)
	case "backfill-prices":
		return cmdBackfillPrices(ctx, st, cmdArgs)
	case "unpriced":
		return cmdUnpriced(st)
	case "repair-finishes":
		return cmdRepairFinishes(ctx, st)
	case "arbitrage":
		return cmdArbitrage(ctx, st, cmdArgs)
	case "catalog":
		return cmdCatalog(ctx, cmdArgs)
	case "binder":
		return cmdBinder(st, cmdArgs)
	case "export":
		return cmdExport(st, cmdArgs)
	case "import":
		return cmdImport(ctx, st, cmdArgs)
	case "deck":
		return cmdDeck(ctx, st, cmdArgs)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// extractDBFlag pulls the global --db flag out of args wherever it appears.
//
// flag stops parsing at the first positional, so a --db written after the command
// would be passed on to a subcommand that ignores it and opens the default
// database — an invisible failure that reports perfectly good numbers about the
// wrong hoard. Only --db is global; other flags stay for the subcommand.
func extractDBFlag(args []string) (rest []string, db string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// By convention everything after a bare "--" is positional.
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}

		name, value, hasValue := strings.Cut(arg, "=")
		if name != "-db" && name != "--db" {
			rest = append(rest, arg)
			continue
		}

		if !hasValue {
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
			}
			i++
			value = args[i]
		}
		if value == "" {
			return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
		}
		db = value // a repeated --db keeps the last, as the flag package does
	}
	return rest, db, nil
}

// defaultDBPath resolves where the hoard database lives when --db is not given.
// Precedence: $HOARD_DB, else a per-user application-data directory so the same
// hoard is used regardless of the current working directory.
func defaultDBPath() (string, error) {
	if p := os.Getenv("HOARD_DB"); p != "" {
		return p, nil
	}
	dir, err := dataDir()
	if err != nil {
		return "", fmt.Errorf("locating data directory (set --db or $HOARD_DB): %w", err)
	}
	return filepath.Join(dir, "hoard", "hoard.db"), nil
}

// dataDir returns the platform's per-user data directory: the conventional
// location on macOS/Windows and the XDG Base Directory spec elsewhere. Unlike a
// cache directory, this holds data that must not be evicted — the hoard is not
// re-downloadable.
func dataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if p := os.Getenv("AppData"); p != "" {
			return p, nil
		}
	default: // linux, bsd, etc.
		if p := os.Getenv("XDG_DATA_HOME"); p != "" {
			return p, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// parsePositionals parses args, allowing flags and positional arguments to be
// interleaved in any order (the standard library's flag package otherwise stops
// at the first positional). It returns the collected positional arguments.
func parsePositionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// stdinIsTTY reports whether stdin is an interactive terminal (a character
// device), which the TUI requires.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// cmdBrowse is what `hoard` with no arguments does: the browser at a terminal,
// the summary table when piped, so `hoard | grep` keeps working.
//
// The loop is the add handoff — `a` quits with a request, we run the cascade, then
// re-enter. Two bubbletea programs cannot share a terminal, so they take turns.
func cmdBrowse(ctx context.Context, st *store.Store) error {
	if !stdoutIsTTY() {
		return writeSummary(st)
	}
	// arbitrageMin matches the CLI command's --min default, so the two views
	// agree about what is too cheap to be worth a shipping label.
	const arbitrageMin = 1.0

	for {
		again, err := browse.Run(ctx, st,
			browse.WithArbitrage(func(ctx context.Context) (arbitrage.Result, error) {
				return fetchArbitrage(ctx, st, arbitrageMin)
			}))
		if err != nil || !again {
			return err
		}
		// The cascade persists each confirmed card itself, so there is nothing
		// to hand back; the browser re-reads on the next pass.
		if err := addByName(ctx, st, ""); err != nil {
			// A failed add is not a reason to lose the browser.
			fmt.Fprintln(os.Stderr, "add:", err)
		}
	}
}

// writeSummary prints the hoard's totals, the output `hoard summary` used to
// produce. It is what a non-interactive `hoard` writes.
func writeSummary(st *store.Store) error {
	coll, err := st.CollectionTotals()
	if err != nil {
		return err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return err
	}
	fmt.Print(report.Summary(ui.Detect(os.Stdout), coll, decks))
	return nil
}

// stdoutIsTTY reports whether output is going to an interactive terminal rather
// than a pipe or a file.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
