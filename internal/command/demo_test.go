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

	for _, c := range changes {
		if c.Copies == 0 {
			t.Errorf("%s (%s) reports %d copies; movers should only carry held rows",
				c.Name, c.Finish, c.Copies)
		}
	}
}

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

func TestDemoDBLivesInTheCacheDirectory(t *testing.T) {

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
