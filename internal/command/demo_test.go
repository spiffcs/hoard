package command

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/demodata"
	"github.com/spiffcs/hoard/internal/store"
)

// The safety property the whole command rests on. Without the annotation the
// root's PersistentPreRunE opens — and on a machine that has none, creates —
// the user's real hoard before demo's RunE is ever reached, so asking to see a
// sample collection would have the side effect of establishing a real one.
//
// This is asserted rather than trusted because nothing else would notice:
// `hoard demo` would still work, still seed its own database, still show the
// sample. The unwanted file appears somewhere the test output never looks.
func TestDemoRunsWithoutTheUsersDatabase(t *testing.T) {
	root, _ := buildRoot(&app{env: bufEnv(io.Discard)}, pipeEnv)

	for _, c := range root.Commands() {
		if c.Name() == "demo" {
			if !cli.Has(c, cli.AnnotationNoStore) {
				t.Fatal("demo is not marked NoStore: running it would open the user's hoard")
			}
			return
		}
	}
	t.Fatal("no demo command in the tree")
}

// Seeding is the demo's whole content, and it goes through the merge planner.
// A change there that broke a fresh seed would otherwise surface as a first-run
// experience nobody on the project ever repeats — everyone developing hoard
// already has a demo database.
func TestSeedHoardPopulatesAnEmptyStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	res, err := action.SeedHoard(st, demodata.Collection, "the sample collection")
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if res.Printings == 0 || res.Copies == 0 {
		t.Fatalf("seed put in %d printings and %d copies; want both non-zero", res.Printings, res.Copies)
	}

	binders, err := st.ListBinders()
	if err != nil {
		t.Fatal(err)
	}
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatal(err)
	}
	if len(binders) == 0 || len(decks) == 0 {
		t.Errorf("seeded store has %d binders and %d decks; want at least one of each",
			len(binders), len(decks))
	}
}

// The demo database is deliberately in the cache directory, not the data
// directory: one is documented as safe to delete and rebuildable, the other
// holds the collection and must never be evicted. Putting the demo in dataDir
// would be a quiet mistake — it would work perfectly.
func TestDemoDBLivesInTheCacheDirectory(t *testing.T) {
	demo, err := demoDBPath()
	if err != nil {
		t.Skipf("no cache directory on this machine: %v", err)
	}
	data, err := dataDir()
	if err != nil {
		t.Skipf("no data directory on this machine: %v", err)
	}
	if strings.HasPrefix(demo, data) {
		t.Errorf("demo database %q is under the data directory %q, where nothing is safe to evict", demo, data)
	}
	if filepath.Base(demo) == "hoard.db" {
		t.Errorf("demo database is named %q — too easy to mistake for the real one", filepath.Base(demo))
	}
}
