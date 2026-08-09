package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Deck add runs through the shared resolver: offline-testable, and a
// set+number Scryfall does not know retries by name instead of dropping the
// card. Neither was true when deck add carried its own copy of the pipeline.
func TestDeckAddResolvesOfflineWithNameRetry(t *testing.T) {
	st := importStore(t)
	calls := stubFetch(t, importFixtures()...)

	list := filepath.Join(t.TempDir(), "Fish Tank.txt")
	// The Sol Ring line names a set/number the fixture does not know — only
	// the name retry can save it.
	if err := os.WriteFile(list, []byte("2 Sol Ring (zzz) 999\n1 Mystic Remora\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list}, false); err != nil {
		t.Fatalf("hoard deck add: %v", err)
	}
	if *calls != 2 {
		t.Errorf("fetch called %d times, want 2 (bulk pass + name retry)", *calls)
	}

	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 {
		t.Fatalf("decks = %+v, %v", decks, err)
	}
	if decks[0].TotalCopies != 3 {
		t.Errorf("deck holds %d copies, want 3 — the retried Sol Ring must not be dropped", decks[0].TotalCopies)
	}
}

// A deck that imports with skipped cards reports partial completion through
// the exit code, so a script can tell "done" from "done, mostly".
func TestDeckAddPartialResolutionIsExitCodeTwo(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)

	list := filepath.Join(t.TempDir(), "ghosts.txt")
	if err := os.WriteFile(list, []byte("1 Mystic Remora\n1 Card That Does Not Exist\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list}, false)
	if !errors.Is(err, errPartial) {
		t.Fatalf("err = %v, want errPartial", err)
	}
	// The resolvable card still landed: partial is "done, mostly", not "failed".
	decks, _ := st.ListDecks()
	if len(decks) != 1 || decks[0].TotalCopies != 1 {
		t.Errorf("decks = %+v, want the remora imported despite the ghost", decks)
	}
}
