// Command hoard catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/browse"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const usage = `hoard — catalog valuable MTG cards and decks in SQLite

Usage:
  hoard [--db PATH] [--json] [command] [args]

  hoard                                            Browse the hoard (no arguments)

Browsing replaces the old list, summary, deck list and deck show commands: it
shows your binder and every deck beside their cards, filters by name or card trait, edits
quantities in place, and reaches movers, unpriced and arbitrage with 'v'. Piped
or redirected, plain 'hoard' prints the summary table instead, so 'hoard | grep'
still works.

Collection commands:
  add                                              Add cards interactively by name
  add <scryfall-url> [--foil] [--qty N]            Add one card by its Scryfall link
  add --file LIST | - [--binder B] [--again]       Add a pasted/exported card list (or pipe one in)
  update-prices [--limit N]                        Refresh prices (Scryfall updates daily)
  movers [--since 30d] [--limit N]                 Biggest risers and sinkers you hold
  backfill-prices                                  Load 90 days of past prices from MTGJSON
  unpriced                                         Cards counting as $0.00, and why
  repair-finishes                                  Fix cards stored as a finish they lack
  arbitrage [--min N] [--limit N]                  Where vendors disagree, as three tables
  report [--top N] [--csv] [-o FILE]               Dated valuation: totals, binders, top holdings
  watch                                            Check price watches (no network; exit 3 = fired)
  watch add <name> --under N|--over N [--foil]     Alert when a price crosses a threshold
  watch list | watch rm <id|name>                  Your watches, and removing one
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
  export [--binder B | --deck D | --all] [-o FILE] Holdings as CSV or JSON (everything by
         [--format csv|json|moxfield|archidekt]       default) in hoard's format or theirs
  import FILE [--binder B | --preserve-binders]    Add a collection CSV export (ManaBox,
         [--format F] [--dry-run]                    Moxfield, Delver Lens, hoard)

A deck <name> can be any part of its name, as long as it matches one deck.

--json prints a versioned JSON document instead of a table, on the read
commands: hoard (the summary), unpriced, movers, arbitrage, report, watch,
and export. See docs/json.md; the schemas live in schema/json/.

Exit codes: 0 success · 1 error · 2 finished but skipped items (e.g. an import
with unresolvable cards) · 3 a price watch crossed its threshold — so a script
can tell "done" from "done, mostly", and a cron can branch on an alert.

The database lives in a per-user data directory by default (e.g. on macOS
~/Library/Application Support/hoard/hoard.db, on Linux $XDG_DATA_HOME/hoard/hoard.db)
— so it's the same hoard from any directory. Override with --db or $HOARD_DB.
Moxfield's API is Cloudflare-blocked; export that deck to text and use 'deck add --file'.
`

// errPartial is the action layer's partial-completion sentinel, aliased so
// every command in this package wraps the same value main maps to exit 2.
var errPartial = action.ErrPartial

func main() {
	if err := run(os.Args[1:]); err != nil {
		// A fired watch is a result, not a failure: the alert is already on
		// stdout, and the sentinel only carries the exit status a cron
		// wrapper branches on.
		if errors.Is(err, errWatchFired) {
			os.Exit(3)
		}
		// An interrupt is the user's own doing; echoing a stack of wrapped
		// context errors at them would bury the one word that matters.
		// 130 is the shell convention for died-on-SIGINT.
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, errPartial) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	rest, dbFlag, err := extractDBFlag(args)
	if err != nil {
		fmt.Fprint(os.Stderr, usage)
		return err
	}
	rest, jsonOut := extractJSONFlag(rest)

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

	// Ctrl-C cancels the context instead of killing the process, so a
	// half-done operation unwinds through its own error paths — every write
	// is a single transaction or a temp-file rename, so cancellation leaves
	// nothing torn. The handler unregisters itself once fired: a second
	// ctrl-c gets the default treatment and kills a hung unwind instantly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	switch cmd {
	case "":
		return cmdBrowse(ctx, st, jsonOut)
	case "movers":
		return cmdMovers(st, cmdArgs, jsonOut)
	case "unpriced":
		return cmdUnpriced(st, jsonOut)
	case "arbitrage":
		return cmdArbitrage(ctx, st, cmdArgs, jsonOut)
	case "report":
		return cmdReport(st, cmdArgs, jsonOut)
	case "export":
		return cmdExport(st, cmdArgs, jsonOut)
	case "watch":
		// The bare check emits JSON; cmdWatch itself rejects --json on the
		// add/list/rm subcommands, which write or configure.
		return cmdWatch(ctx, st, cmdArgs, jsonOut)
	}
	// Everything below writes or configures; a --json that would be silently
	// ignored is worse than an error, because the caller is a script that
	// expects to parse what comes back.
	if jsonOut {
		return fmt.Errorf("%s has no JSON output; --json works on: hoard, unpriced, movers, arbitrage, report, watch, export", cmd)
	}
	switch cmd {
	case "add":
		return cmdAdd(ctx, st, cmdArgs)
	case "update-prices":
		return cmdUpdatePrices(ctx, st, cmdArgs)
	case "backfill-prices":
		return cmdBackfillPrices(ctx, st, cmdArgs)
	case "repair-finishes":
		return cmdRepairFinishes(ctx, st)
	case "catalog":
		return cmdCatalog(ctx, cmdArgs)
	case "binder":
		return cmdBinder(st, cmdArgs)
	case "import":
		return cmdImport(ctx, st, cmdArgs)
	case "deck":
		return cmdDeck(ctx, st, cmdArgs)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// extractJSONFlag pulls the global --json switch out of args wherever it
// appears, for the same reason extractDBFlag exists: written after the
// command, it would be passed to a subcommand that ignores it, and the caller
// — by definition a script — would try to parse a table.
func extractJSONFlag(args []string) (rest []string, jsonOut bool) {
	rest = make([]string, 0, len(args))
	for i, arg := range args {
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if arg == "-json" || arg == "--json" {
			jsonOut = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, jsonOut
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

// isTTY reports whether a file is an interactive terminal (a character
// device).
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// stdinIsTTY reports whether stdin is interactive, which the TUI requires.
func stdinIsTTY() bool { return isTTY(os.Stdin) }

// cmdBrowse is what `hoard` with no arguments does: the browser at a terminal,
// the summary table when piped, so `hoard | grep` keeps working.
//
// The loop is the add handoff — `a` quits with a request, we run the cascade, then
// re-enter. Two bubbletea programs cannot share a terminal, so they take turns.
func cmdBrowse(ctx context.Context, st *store.Store, jsonOut bool) error {
	// --json means the summary document even at a terminal: asking for JSON is
	// asking for output, not for an interactive session.
	if jsonOut {
		return writeSummary(st, true)
	}
	if !stdoutIsTTY() {
		return writeSummary(st, false)
	}
	// arbitrageMin matches the CLI command's --min default, so the two views
	// agree about what is too cheap to be worth a shipping label.
	const arbitrageMin = 1.0

	// The catalog handle outlives each browse pass; the injected operations
	// share it. Deps.Confirm stays nil inside the TUI — a bare download
	// question cannot pop over an alt screen yet, and nil declines, which
	// falls through to the API exactly as a piped run does.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	deps := action.Deps{
		Store: st, Catalog: cat, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver,
	}

	for {
		again, err := browse.Run(ctx, st,
			browse.WithArbitrage(func(ctx context.Context, p progress.Fn) (arbitrage.Result, error) {
				return action.Arbitrage(ctx, deps, p, arbitrageMin)
			}),
			browse.WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
				res, err := action.UpdatePrices(ctx, deps, p)
				if err != nil {
					return "", err
				}
				if res.Total == 0 {
					return "no cards yet; nothing to update", nil
				}
				s := fmt.Sprintf("prices updated · %s printings", ui.Count(res.Found))
				if res.Gaps.Remaining > 0 {
					s += fmt.Sprintf(" · %d still unpriced", res.Gaps.Remaining)
				}
				return s, nil
			}),
			browse.WithRepairFinishes(func(ctx context.Context, p progress.Fn) (string, error) {
				res, err := action.RepairFinishes(ctx, deps, p)
				if err != nil {
					return "", err
				}
				switch {
				case res.Total == 0:
					return "no cards yet; nothing to repair", nil
				case len(res.Fixed) == 0 && len(res.Ambiguous) == 0:
					return "every finish already correct", nil
				case len(res.Ambiguous) > 0:
					return fmt.Sprintf("finishes repaired · %d fixed · %d ambiguous (see hoard repair-finishes)",
						len(res.Fixed), len(res.Ambiguous)), nil
				}
				return fmt.Sprintf("finishes repaired · %d fixed", len(res.Fixed)), nil
			}),
			browse.WithCatalogUpdate(func(ctx context.Context, p progress.Fn) (string, error) {
				res, err := action.CatalogUpdate(ctx, deps, p)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("catalog ready · %s cards", ui.Count(res.Cards)), nil
			}),
			browse.WithBackfill(func(ctx context.Context, p progress.Fn) (string, error) {
				res, err := action.BackfillPrices(ctx, deps, p)
				if err != nil {
					return "", err
				}
				switch {
				case res.Printings == 0:
					return "nothing owned yet", nil
				case res.Inserted == 0:
					return "nothing to backfill · history already recorded", nil
				}
				return fmt.Sprintf("backfilled %s observations across %s printings",
					ui.Count(res.Inserted), ui.Count(res.Cards)), nil
			}),
			browse.WithWatchAddByName(func(ctx context.Context, p progress.Fn,
				name, op string, threshold float64) (string, error) {
				res, err := action.WatchAdd(ctx, deps, p,
					action.WatchAddOptions{Name: name, Op: op, Threshold: threshold})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("watching %s (%s) %s %s",
					res.Card.Name, res.Finish, op, ui.Money(threshold)), nil
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
func writeSummary(st *store.Store, jsonOut bool) error {
	sum, err := action.Deps{Store: st}.Summary()
	if err != nil {
		return err
	}
	if jsonOut {
		return hoardjson.Write(os.Stdout, hoardjson.FromSummary(sum.Binder, sum.Decks))
	}
	fmt.Print(report.Summary(ui.Detect(os.Stdout), sum.Binder, sum.Decks))
	return nil
}

// stdoutIsTTY reports whether output is going to an interactive terminal rather
// than a pipe or a file.
func stdoutIsTTY() bool { return isTTY(os.Stdout) }
