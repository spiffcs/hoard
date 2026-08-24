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
					"it writes to the database", path)
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

func TestReadingCommandsWorkOnACompendium(t *testing.T) {
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
				t.Errorf("hoard %v failed on a compendium: %v", args, err)
			}
		})
	}
}

func TestCompendiumAcceptsWrites(t *testing.T) {
	ctx := context.Background()

	for _, args := range [][]string{
		{"binder", "new", "Trades"},
		{"vacuum"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := execCmd(ctx, compendiumStore(t), args, false); err != nil {
				t.Fatalf("hoard %v was refused on a compendium: %v", args, err)
			}
		})
	}
}

func TestCompendiumBuildsADeckSoItCanBePriced(t *testing.T) {
	st := compendiumStore(t)
	if _, err := execCmd(context.Background(), st, []string{"binder", "new", "Premodern"}, false); err != nil {
		t.Fatalf("a compendium must take a binder so decklists can be priced against it: %v", err)
	}
	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	found := false
	for _, b := range binders {
		if b.Name == "Premodern" {
			found = true
		}
	}
	if !found {
		t.Errorf("the binder was not written; got %+v", binders)
	}
}
