package command

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
)

func compendiumStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetCompendiumMode(true); err != nil {
		t.Fatalf("SetCompendiumMode: %v", err)
	}
	return st
}

func TestMutatingCommandsRefuseACatalogDatabase(t *testing.T) {
	ctx := context.Background()

	for _, args := range [][]string{
		{"vacuum"},
		{"backfill-prices"},
		{"update-prices"},
		{"repair-finishes"},
		{"binder", "new", "Trades"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := execCmd(ctx, compendiumStore(t), args, false)
			if err == nil {
				t.Fatalf("hoard %v succeeded against a compendium database", args)
			}
			if !strings.Contains(err.Error(), "compendium") {
				t.Errorf("error = %q, want it to name the compendium database as the reason", err)
			}
		})
	}
}

func TestEveryMutatingCommandIsAnnotated(t *testing.T) {
	root, _ := buildRoot(&app{env: bufEnv(io.Discard)}, pipeEnv)

	want := []string{
		"add", "update-prices", "backfill-prices", "repair-finishes", "vacuum",
		"binder new", "binder rename", "binder rm",
		"deck add", "deck repin", "deck remove",
		"watch add", "watch import", "watch rm",
		"import", "merge",
	}
	for _, path := range want {
		t.Run(path, func(t *testing.T) {
			cmd := find(t, root, strings.Fields(path))
			if !cli.Has(cmd, cli.AnnotationMutating) {
				t.Errorf("%q writes to the database but is not marked mutating; "+
					"it would run against a catalog", path)
			}
		})
	}

	for _, path := range []string{"movers", "report", "unpriced", "refused",
		"export", "binder list", "watch list"} {
		t.Run("read-only "+path, func(t *testing.T) {
			if cmd := find(t, root, strings.Fields(path)); cli.Has(cmd, cli.AnnotationMutating) {
				t.Errorf("%q only reads but is marked mutating", path)
			}
		})
	}
}

func find(t *testing.T, root *cobra.Command, path []string) *cobra.Command {
	t.Helper()
	cmd, _, err := root.Find(path)
	if err != nil || cmd == nil || cmd == root {
		t.Fatalf("no such command %v (%v)", path, err)
	}
	return cmd
}

func TestReadOnlyCommandsStillWorkOnACatalogDatabase(t *testing.T) {
	ctx := context.Background()

	for _, args := range [][]string{
		{"movers"},
		{"report"},
		{"unpriced"},
		{"binder", "list"},
		{"export"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := execCmd(ctx, compendiumStore(t), args, false); err != nil {
				t.Errorf("hoard %v failed on a catalog database: %v", args, err)
			}
		})
	}
}
