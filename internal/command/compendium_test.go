package command

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/compendium"
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

type builtCompendium struct {
	called bool
	path   string
	opts   compendium.Options
}

func runCompendiumCmd(t *testing.T, args ...string) (*builtCompendium, error) {
	t.Helper()
	built := &builtCompendium{}
	a := &app{
		env: bufEnv(io.Discard),
		buildCompendium: func(_ context.Context, path string, o compendium.Options) error {
			built.called, built.path, built.opts = true, path, o
			return nil
		},
	}
	root, _ := buildRoot(a, pipeEnv)
	root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.SetArgs(append([]string{"compendium"}, args...))
	return built, root.ExecuteContext(context.Background())
}

func TestCompendiumRunsWithoutTheUsersDatabase(t *testing.T) {
	root, _ := buildRoot(&app{env: bufEnv(io.Discard)}, pipeEnv)

	for _, c := range root.Commands() {
		if c.Name() == "compendium" {
			if !cli.Has(c, cli.AnnotationNoStore) {
				t.Fatal("compendium is not marked NoStore: building one would open the user's hoard")
			}
			return
		}
	}
	t.Fatal("no compendium command in the tree")
}

func TestCompendiumRefusesToBuildWithoutAFilter(t *testing.T) {
	out := filepath.Join(t.TempDir(), "everything.db")

	built, err := runCompendiumCmd(t, out)
	if err == nil {
		t.Fatal("an unfiltered build downloads every paper printing; it must be refused")
	}
	if built.called {
		t.Error("the build started before the filter check")
	}
	for _, want := range []string{"--rarity", "--sets", "--format", "--since", "--all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s, got %q", want, err)
		}
	}
	if strings.Contains(err.Error(), "--legal") {
		t.Errorf("the refusal should not advertise a flag that no longer exists, got %q", err)
	}
}

func TestAllBuildsEverythingOnPurpose(t *testing.T) {
	out := filepath.Join(t.TempDir(), "everything.db")

	built, err := runCompendiumCmd(t, "--all", out)
	if err != nil {
		t.Fatalf("--all is the way to ask for everything: %v", err)
	}
	if !built.called {
		t.Fatal("--all did not reach the build")
	}
	if built.opts.Legal != "" || len(built.opts.Sets) != 0 || len(built.opts.Rarities) != 0 {
		t.Errorf("--all must not invent a filter, got %+v", built.opts)
	}
}

func TestCompendiumRefusesToWriteOverAnExistingFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hoard.db")
	if err := os.WriteFile(out, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	built, err := runCompendiumCmd(t, "--format", "premodern", out)
	if err == nil {
		t.Fatal("seeding into an existing database would corrupt a real collection")
	}
	if built.called {
		t.Error("the build started before the existing file was noticed")
	}
	if !strings.Contains(err.Error(), out) {
		t.Errorf("the refusal should name the path, got %q", err)
	}
}

func TestFlagsReachTheBuildOptions(t *testing.T) {
	out := filepath.Join(t.TempDir(), "legacy.db")

	built, err := runCompendiumCmd(t,
		"--format", "legacy", "--rarity", "mythic", "--sets", "mh2,c21",
		"--since", "2020", "--days", "45", "--priced-only", out)
	if err != nil {
		t.Fatalf("compendium: %v", err)
	}
	if !built.called {
		t.Fatal("the build never ran")
	}
	if built.path != out {
		t.Errorf("path = %q, want %q", built.path, out)
	}
	if built.opts.Legal != "legacy" {
		t.Errorf("Legal = %q, want legacy", built.opts.Legal)
	}
	if !slices.Equal(built.opts.Rarities, []string{"mythic"}) {
		t.Errorf("Rarities = %v, want [mythic]", built.opts.Rarities)
	}
	if !slices.Equal(built.opts.Sets, []string{"mh2", "c21"}) {
		t.Errorf("Sets = %v, want [mh2 c21]", built.opts.Sets)
	}
	if built.opts.Since != 2020 || built.opts.Days != 45 || !built.opts.PricedOnly {
		t.Errorf("Since/Days/PricedOnly = %d/%d/%v, want 2020/45/true",
			built.opts.Since, built.opts.Days, built.opts.PricedOnly)
	}
}

func TestAnUnknownFormatIsRejectedBeforeAnythingDownloads(t *testing.T) {
	out := filepath.Join(t.TempDir(), "typo.db")

	built, err := runCompendiumCmd(t, "--format", "moddern", out)
	if err == nil {
		t.Fatal("a misspelled format must be rejected")
	}
	if built.called {
		t.Error("the build started despite an unknown format")
	}
	if !strings.Contains(err.Error(), "legacy") {
		t.Errorf("the error should list the formats that exist, got %q", err)
	}
}

func TestTheLegalFlagIsGone(t *testing.T) {
	out := filepath.Join(t.TempDir(), "legacy.db")

	built, err := runCompendiumCmd(t, "--legal", "legacy", out)
	if err == nil {
		t.Fatal("--legal and --format meant the same thing for every format; " +
			"only --format should remain")
	}
	if built.called {
		t.Error("the build ran on a flag that should no longer exist")
	}
	if !strings.Contains(err.Error(), "legal") {
		t.Errorf("the error should name the flag it rejected, got %q", err)
	}
}

func TestFormatKeepsReprintsUnlessTheEraIsAsked(t *testing.T) {
	out := filepath.Join(t.TempDir(), "premodern.db")

	built, err := runCompendiumCmd(t, "--format", "premodern", out)
	if err != nil {
		t.Fatalf("compendium: %v", err)
	}
	if !built.called {
		t.Fatal("the build never ran")
	}
	if built.opts.Legal != "premodern" {
		t.Errorf("Legal = %q, want premodern", built.opts.Legal)
	}
	if len(built.opts.Sets) != 0 {
		t.Errorf("a modern reprint of a premodern-legal card is legal to play and is "+
			"often the cheapest copy, so --format alone must keep it; got sets %v",
			built.opts.Sets)
	}
}

func TestEraPinsTheSetsAtTheFlagSurface(t *testing.T) {
	out := filepath.Join(t.TempDir(), "premodern-era.db")

	built, err := runCompendiumCmd(t, "--format", "premodern", "--era", out)
	if err != nil {
		t.Fatalf("compendium: %v", err)
	}
	if !built.called {
		t.Fatal("the build never ran")
	}
	if len(built.opts.Sets) != 29 {
		t.Errorf("--era should pin the 29 sets from Fourth Edition through Scourge, got %v",
			built.opts.Sets)
	}
	if !slices.Contains(built.opts.Sets, "scg") || slices.Contains(built.opts.Sets, "mh2") {
		t.Errorf("--era pinned the wrong sets: %v", built.opts.Sets)
	}
}

func TestEraNeedsAFormatToTakeItsSetsFrom(t *testing.T) {
	out := filepath.Join(t.TempDir(), "era.db")

	built, err := runCompendiumCmd(t, "--era", "--rarity", "rare", out)
	if err == nil {
		t.Fatal("--era without --format names no era and must be refused")
	}
	if built.called {
		t.Error("the build started despite --era having no format")
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Errorf("the refusal should point at --format, got %q", err)
	}
}

func TestEraIsRefusedForAFormatThatHasNoEra(t *testing.T) {
	out := filepath.Join(t.TempDir(), "legacy-era.db")

	built, err := runCompendiumCmd(t, "--format", "legacy", "--era", out)
	if err == nil {
		t.Fatal("legacy has no bounded set list, so --era must be refused rather than ignored")
	}
	if built.called {
		t.Error("the build started despite an era that does not exist")
	}
	if !strings.Contains(err.Error(), "premodern") {
		t.Errorf("the refusal should name the formats that do have an era, got %q", err)
	}
}

func TestPredhEraReachesTheBuildOptionsAsADateBound(t *testing.T) {
	out := filepath.Join(t.TempDir(), "predh-era.db")

	built, err := runCompendiumCmd(t, "--format", "predh", "--era", out)
	if err != nil {
		t.Fatalf("compendium: %v", err)
	}
	if !built.called {
		t.Fatal("the build never ran")
	}
	if built.opts.Legal != "predh" {
		t.Errorf("Legal = %q, want predh", built.opts.Legal)
	}
	if built.opts.Before != "2011-06-17" {
		t.Errorf("Before = %q, want 2011-06-17", built.opts.Before)
	}
	if len(built.opts.Sets) != 0 {
		t.Errorf("PreDH bounds by release date, not by a set list; got %v", built.opts.Sets)
	}
}

func TestPredhWithoutTheEraFlagKeepsReprints(t *testing.T) {
	out := filepath.Join(t.TempDir(), "predh.db")

	built, err := runCompendiumCmd(t, "--format", "predh", out)
	if err != nil {
		t.Fatalf("compendium: %v", err)
	}
	if built.opts.Before != "" || len(built.opts.Sets) != 0 {
		t.Errorf("--format predh alone must not bound the era, got Before=%q Sets=%v",
			built.opts.Before, built.opts.Sets)
	}
}
