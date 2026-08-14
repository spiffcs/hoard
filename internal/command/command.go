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

var errPartial = action.ErrPartial

func Run(args []string) int {
	err := execute(args)
	if err == nil {
		return 0
	}

	if errors.Is(err, errWatchFired) {
		return 3
	}

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

type globals struct {
	db   string
	json bool
}

func buildRoot(a *app, envFor func(io.Writer) ui.Env) (*cobra.Command, *globals) {
	g := &globals{}
	root := rootCommand(a)
	root.PersistentFlags().StringVar(&g.db, "db", "",
		"the hoard database to use (default $HOARD_DB, else the per-user data dir)")

	root.PersistentFlags().BoolVar(&g.json, cli.FlagNameJSON, false,
		"emit JSON instead of tables, where the command supports it")

	root.SetOut(a.env.Out)
	root.SetErr(a.env.Err)

	cli.InstallHelp(root, tagline, envFor)
	return root, g
}

func detectEnv(w io.Writer) ui.Env {
	if f, ok := w.(*os.File); ok {
		return ui.Detect(f)
	}
	return ui.Env{Width: 80}
}

func execute(args []string) error {

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

		if cmd == root && cmd.Flags().Changed("version") {
			return nil
		}

		dbPath := g.db
		if dbPath == "" {
			var err error
			if dbPath, err = defaultDBPath(); err != nil {
				return err
			}
		}

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
	default:
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
