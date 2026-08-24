package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func emptyStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestReadingCommandsRun(t *testing.T) {
	ctx := context.Background()

	for _, args := range [][]string{
		{"movers"},
		{"report"},
		{"unpriced"},
		{"binder", "list"},
		{"export"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := execCmd(ctx, emptyStore(t), args, false); err != nil {
				t.Errorf("hoard %v failed: %v", args, err)
			}
		})
	}
}

func TestMutatingCommandsRun(t *testing.T) {
	ctx := context.Background()

	for _, args := range [][]string{
		{"binder", "new", "Trades"},
		{"vacuum"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := execCmd(ctx, emptyStore(t), args, false); err != nil {
				t.Fatalf("hoard %v failed: %v", args, err)
			}
		})
	}
}

func TestCompendiumBuildsADeckSoItCanBePriced(t *testing.T) {
	st := emptyStore(t)
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
