// Package command is hoard's command tree: every command the binary can run,
// and the wiring that turns an argv into one of them.
//
// It is a package rather than the module's main so that the entry point under
// cmd/hoard holds nothing but the call into Run — the same split crane makes,
// for the same reason: a command tree is ordinary code and benefits from being
// importable and testable, while a main function cannot be either.
//
// This package is the commands themselves. Its neighbor internal/cli is the
// support layer beneath them — Env, Usagef, the annotations, and the help
// renderer — kept separate because that layer is about what cobra does not
// supply, not about what hoard does.
package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// errPartial is the action layer's partial-completion sentinel, aliased so
// every command in this package wraps the same value Run maps to exit 2.
var errPartial = action.ErrPartial

// Run executes hoard for the given arguments and returns the process exit code.
//
// Exit codes are decided here and nowhere else. Cobra's Execute hands back an
// error; this is the single place that reads it and says what status a script
// should see. Returning the code rather than calling os.Exit keeps that
// decision testable, and lets the sentinels stay unexported next to the code
// that raises them.
func Run(args []string) int {
	err := execute(args)
	if err == nil {
		return 0
	}
	// A fired watch is a result, not a failure: the alert is already on
	// stdout, and the sentinel only carries the exit status a cron
	// wrapper branches on.
	if errors.Is(err, errWatchFired) {
		return 3
	}
	// An interrupt is the user's own doing; echoing a stack of wrapped
	// context errors at them would bury the one word that matters.
	// 130 is the shell convention for died-on-SIGINT.
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "interrupted")
		return 130
	}
	fmt.Fprintln(os.Stderr, ui.Detect(os.Stderr).Err()("error:"), err)
	if errors.Is(err, errPartial) {
		return 2
	}
	return 1
}

// globals are the flags every command shares.
//
// Real persistent flags, so they work wherever they are written. The two
// hand-rolled argv pre-scanners this replaces had to run before any parsing
// and could not know which flags took values, so a --db or --json appearing
// as another flag's value was silently stolen.
type globals struct {
	db   string
	json bool
}

// buildRoot assembles the command tree with its global flags, its help renderer
// and its streams.
//
// envFor is a parameter rather than a constant so a test can build the very
// tree the binary builds instead of assembling a near-copy beside it. The
// near-copy is how a flag declared here goes quietly missing under test —
// crane's New takes its use/short/options for the same reason, so gcrane can
// reuse the tree rather than restate it.
func buildRoot(a *app, envFor func(io.Writer) ui.Env) (*cobra.Command, *globals) {
	g := &globals{}
	root := rootCommand(a)
	root.PersistentFlags().StringVar(&g.db, "db", "",
		"the hoard database to use (default $HOARD_DB, else the per-user data dir)")
	// cli.FlagNameJSON rather than a literal: the help renderer hides this flag
	// on the commands CheckJSON would reject, and it can only match it by name.
	root.PersistentFlags().BoolVar(&g.json, cli.FlagNameJSON, false,
		"emit JSON instead of tables, where the command supports it")

	// Help renders through cobra's streams; commands write through the Env's.
	// They are the same streams, and binding them here is what stops the two
	// paths from drifting — in production they used to agree only because
	// nothing ever called SetOut and both fell back to os.Stdout.
	root.SetOut(a.env.Out)
	root.SetErr(a.env.Err)

	cli.InstallHelp(root, tagline, envFor)
	return root, g
}

// detectEnv is the production stream resolver: a real file is asked for the
// terminal's shape, anything else gets the piped rendering.
func detectEnv(w io.Writer) ui.Env {
	if f, ok := w.(*os.File); ok {
		return ui.Detect(f)
	}
	return ui.Env{Width: 80}
}

func execute(args []string) error {
	// The app is built empty and filled in by PersistentPreRunE: the tree has
	// to exist before --db can be parsed, and the store cannot be opened until
	// after. Every RunE closes over this pointer, so it sees the late arrival.
	a := &app{env: &cli.Env{
		Out: os.Stdout, Err: os.Stderr,
		OutEnv: ui.Detect(os.Stdout), ErrEnv: ui.Detect(os.Stderr),
	}}

	root, g := buildRoot(a, detectEnv)

	var closeStore func()
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cli.CheckJSON(cmd, g.json); err != nil {
			return err
		}
		a.env.JSON = g.json
		if cli.Has(cmd, cli.AnnotationNoStore) {
			return nil
		}
		// `hoard --version` answers from the binary; it must not create a
		// database as a side effect of being asked what it is.
		if cmd == root && cmd.Flags().Changed("version") {
			return nil
		}

		// --db wins over $HOARD_DB, which in turn wins over the per-user default.
		dbPath := g.db
		if dbPath == "" {
			var err error
			if dbPath, err = defaultDBPath(); err != nil {
				return err
			}
		}
		// Note whether we are about to create the database, so we can say where
		// it lives the first time.
		_, statErr := os.Stat(dbPath)
		newDB := os.IsNotExist(statErr)

		st, err := store.Open(dbPath)
		if err != nil {
			return err
		}
		a.store, a.dbPath, closeStore = st, dbPath, func() { st.Close() }
		if newDB {
			ui.NewReport().Progress("Initialized hoard database at %s", dbPath)
		}
		return nil
	}

	// Ctrl-C cancels the context instead of killing the process, so a half-done
	// operation unwinds through its own error paths — every write is a single
	// transaction or a temp-file rename, so cancellation leaves nothing torn.
	// The handler unregisters itself once fired: a second ctrl-c gets the
	// default treatment and kills a hung unwind instantly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if closeStore != nil {
		closeStore()
	}
	return err
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
