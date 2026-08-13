package command

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/demo"
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

	res, err := action.SeedHoard(st, demo.Collection, "the sample collection")
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

// seedDemo builds the database `hoard demo` builds — cards and history both —
// so the tests below read what a first run actually opens on.
func seedDemo(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := action.SeedHoard(st, demo.Collection, "the sample collection"); err != nil {
		t.Fatalf("seeding the collection: %v", err)
	}
	if _, err := demo.SeedEmbeddedHistory(st); err != nil {
		t.Fatalf("seeding the history: %v", err)
	}
	return st
}

// The movers view is the reason the history is seeded at all: it charts a card
// against its own past, so a demo without one opens the view empty and the only
// way to fill it is a ~150 MB download. This asserts the seeded document
// actually lands as movable history, over the span the document itself covers —
// no calendar involved, so it holds however old the sample gets.
func TestSeededDemoHistoryFeedsMovers(t *testing.T) {
	st := seedDemo(t)

	obs, oldest, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if obs == 0 {
		t.Fatal("the seeded demo has no price history: movers would open empty")
	}
	changes, err := st.Movers(oldest)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatalf("%d observations seeded from %s, but movers reports nothing moved", obs, oldest)
	}
	// Held finishes, since movers joins holdings: a document that seeded only
	// the foil series of non-foil holdings would satisfy every count above and
	// still show an empty view.
	for _, c := range changes {
		if c.Copies == 0 {
			t.Errorf("%s (%s) reports %d copies; movers should only carry held rows",
				c.Name, c.Finish, c.Copies)
		}
	}
}

// Seeding only ever runs on a database that did not exist, so every demo built
// before the history was compiled in would open movers empty forever. The top-up
// is what closes that, and it has to be able to tell "never had history" from
// "the owner backfilled this one for real" — re-seeding the second would put
// frozen sample rows underneath live ones.
func TestDemoHistoryTopsUpAnOlderDemoOnlyOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := action.SeedHoard(st, demo.Collection, "the sample collection"); err != nil {
		t.Fatal(err)
	}

	_, seeded, err := demo.TopUpHistory(st)
	if err != nil {
		t.Fatalf("topping up: %v", err)
	}
	if !seeded {
		t.Fatal("a demo with no history reported nothing seeded")
	}
	obs, _, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if obs == 0 {
		t.Fatal("a demo with no history was left with none")
	}

	// Second call: the history is no longer empty, so it must do nothing at
	// all — not "insert nothing", which the store's collision handling would
	// give it for free, but not run.
	before := obs
	_, seeded, err = demo.TopUpHistory(st)
	if err != nil {
		t.Fatalf("second top-up: %v", err)
	}
	if seeded {
		t.Error("a demo that already has history was seeded again")
	}
	after, _, err := st.PriceHistoryDepth()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("history went from %d to %d observations on a second run; want it left alone",
			before, after)
	}
}

// The sample freezes when it is generated, and the movers windows are measured
// back from today, so the file ages out of them: past ninety days the deepest
// window has nothing left to report and the view is empty again. That is a
// property of the design the owner accepted, not a bug — but it must be loud,
// because the failure mode is a demo that silently shows less than it used to.
func TestDemoHistoryIsFreshEnoughForMovers(t *testing.T) {
	st := seedDemo(t)

	const regen = "regenerate it: go run ././internal/demo/gen internal/demo/history.json"
	for _, days := range []int{30, 90} {
		since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
		changes, err := st.Movers(since)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) > 0 {
			continue
		}
		if days == 90 {
			t.Errorf("the built-in sample history is too old to move anything "+
				"in any window the browser offers; %s", regen)
			continue
		}
		t.Logf("the built-in sample history no longer reaches the %d-day window "+
			"(the browser's default); %s", days, regen)
	}
}

// The demo database is deliberately in the cache directory, not the data
// directory: one is documented as safe to delete and rebuildable, the other
// holds the collection and must never be evicted. Putting the demo in dataDir
// would be a quiet mistake — it would work perfectly.
func TestDemoDBLivesInTheCacheDirectory(t *testing.T) {
	// Named db, not demo: the sample data now lives in a package called demo,
	// and a local of the same name shadows it in a file that imports it.
	db, err := demoDBPath()
	if err != nil {
		t.Skipf("no cache directory on this machine: %v", err)
	}
	data, err := dataDir()
	if err != nil {
		t.Skipf("no data directory on this machine: %v", err)
	}
	if strings.HasPrefix(db, data) {
		t.Errorf("demo database %q is under the data directory %q, where nothing is safe to evict", db, data)
	}
	if filepath.Base(db) == "hoard.db" {
		t.Errorf("demo database is named %q — too easy to mistake for the real one", filepath.Base(db))
	}
}
