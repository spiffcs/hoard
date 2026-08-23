package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func wantsCLIStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateBinder("Want"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	return st
}

func countedFor(t *testing.T, st *store.Store, name string) bool {
	t.Helper()
	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	for _, b := range binders {
		if b.Name == name {
			return b.Counted
		}
	}
	t.Fatalf("no binder named %q", name)
	return false
}

func TestBinderExcludeAndIncludeFromTheCLI(t *testing.T) {
	ctx := context.Background()
	st := wantsCLIStore(t)

	if !countedFor(t, st, "Want") {
		t.Fatal("a new binder should start counted")
	}

	if _, err := execCmd(ctx, st, []string{"binder", "exclude", "Want"}, false); err != nil {
		t.Fatalf("binder exclude: %v", err)
	}
	if countedFor(t, st, "Want") {
		t.Error("binder exclude did not stop the binder counting")
	}

	if _, err := execCmd(ctx, st, []string{"binder", "include", "Want"}, false); err != nil {
		t.Fatalf("binder include: %v", err)
	}
	if !countedFor(t, st, "Want") {
		t.Error("binder include did not restore it")
	}
}

func TestBinderListMarksUncountedBinders(t *testing.T) {
	ctx := context.Background()
	st := wantsCLIStore(t)
	if err := st.AddCardFinish(watchCard(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := execCmd(ctx, st, []string{"binder", "exclude", "Want"}, false); err != nil {
		t.Fatalf("binder exclude: %v", err)
	}

	out, err := execCmd(ctx, st, []string{"binder", "list"}, false)
	if err != nil {
		t.Fatalf("binder list: %v", err)
	}
	if !strings.Contains(out, "Want") {
		t.Fatalf("binder list lost the binder:\n%s", out)
	}
	marked := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Want") {
			marked = line
		}
	}
	if marked == "" || !strings.ContainsAny(marked, "*·") {
		t.Errorf("uncounted binder carries no marker:\n%s", out)
	}
}
