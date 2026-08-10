package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

// deckFile writes a decklist to a temp file and returns its path. The base
// name is the deck's name for --file imports, so it is a parameter rather
// than a fixed "deck.txt": several tests below turn on whether two imports
// are the same deck or two.
func deckFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertNothingWritten fails unless the database holds exactly what a freshly
// opened one holds. It checks all three tables a deck import writes rather
// than just the deck: DeckAdd upserts the printings behind the entries before
// it upserts the deck, and a guard that covered only the deck would leave
// catalog rows behind while still reporting "nothing was written".
func assertNothingWritten(t *testing.T, st *store.Store, when string) {
	t.Helper()
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 0 {
		t.Errorf("%s: wrote %d decks, want none: %+v", when, len(decks), decks)
	}
	if _, printings, err := st.EnrichedCount(); err != nil {
		t.Fatalf("EnrichedCount: %v", err)
	} else if printings != 0 {
		t.Errorf("%s: wrote %d printing rows, want none", when, printings)
	}
	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 0 {
		t.Errorf("%s: wrote %d copies, want none", when, totals.TotalCopies)
	}
}

// A dry run resolves the whole list — the network work is the point, since
// what resolves is the question being asked — and then writes none of it.
// The real import of the same file afterwards proves the rehearsal was of
// something: a --dry-run that quietly did nothing at all would also pass the
// first half of this test.
func TestDeckAddDryRunWritesNothing(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	list := deckFile(t, "Fish Tank.txt", "2 Sol Ring (c21) 125\n1 Mystic Remora\n")

	out, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list, "--dry-run"}, false)
	if err != nil {
		t.Fatalf("hoard deck add --dry-run: %v", err)
	}
	// The headline carries no deck id: none was allocated, and "#0" would
	// name a row that does not exist.
	if !strings.Contains(out, `Would import deck "Fish Tank" (text): 2 cards resolved.`) {
		t.Errorf("dry run headline missing or malformed:\n%s", out)
	}
	if strings.Contains(out, "#") {
		t.Errorf("dry run printed a deck id:\n%s", out)
	}
	// The same closing line import's dry run uses, verbatim.
	if !strings.Contains(out, "Dry run: nothing was written.") {
		t.Errorf("dry run did not say nothing was written:\n%s", out)
	}
	assertNothingWritten(t, st, "dry run")

	// Same file, no --dry-run: the rehearsal predicted this.
	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list}, false); err != nil {
		t.Fatalf("hoard deck add: %v", err)
	}
	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 {
		t.Fatalf("decks = %+v, %v", decks, err)
	}
	if decks[0].TotalCopies != 3 {
		t.Errorf("deck holds %d copies, want the 3 the dry run rehearsed", decks[0].TotalCopies)
	}
}

// The cards that do not resolve are the whole reason to rehearse an
// LLM-authored decklist, so a dry run must name them and must exit 2 — the
// same partial-completion status the real import would exit with, since a
// rehearsal that reported "clean" for an import that will not be would be
// worse than no rehearsal.
func TestDeckAddDryRunNamesUnresolvedAndExitsTwo(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	list := deckFile(t, "ghosts.txt",
		"1 Mystic Remora\n1 Card That Does Not Exist\n~~~ not a decklist line ~~~\n")

	out, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list, "--dry-run"}, false)
	if !errors.Is(err, errPartial) {
		t.Fatalf("err = %v, want errPartial (exit 2)", err)
	}
	if !strings.Contains(err.Error(), "would not resolve") {
		t.Errorf("error is in the past tense for a run that has not happened: %v", err)
	}
	// Both kinds of failure surface: the line the parser could not read and
	// the card the resolver could not find are different problems with the
	// same file, and a rehearsal that showed one but not the other would
	// send the user back for a second round trip.
	for _, want := range []string{
		"1 cards could not be resolved and were skipped:",
		"Card That Does Not Exist",
		"1 lines could not be read and were skipped:",
		"~~~ not a decklist line ~~~",
		"Dry run: nothing was written.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output missing %q; got:\n%s", want, out)
		}
	}
	// A partial dry run is still a dry run: the card that did resolve must
	// not have been written on the way to reporting the one that did not.
	assertNothingWritten(t, st, "partial dry run")
}

// Rehearsing an import over a deck that is already there must not ask
// whether to replace it. There is nothing to replace on this path, and the
// question would hang the unattended rehearsal the flag exists for. The
// collision is reported instead — and the real re-import still refuses
// without --refresh, which is what makes the report worth printing.
func TestDeckAddDryRunOverAnImportedDeckReportsRatherThanAsks(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	list := deckFile(t, "Stompy.txt", "1 Mystic Remora\n")

	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list}, false); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// A wider list under the same name, so a write would be visible as a
	// changed copy count rather than only as a changed timestamp.
	again := deckFile(t, "Stompy.txt", "1 Mystic Remora\n2 Sol Ring (c21) 125\n")
	out, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", again, "--dry-run"}, false)
	if err != nil {
		t.Fatalf("dry run over an imported deck: %v", err)
	}
	if !strings.Contains(out, `Would replace the imported deck "Stompy"`) {
		t.Errorf("dry run did not report the collision:\n%s", out)
	}

	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 {
		t.Fatalf("decks = %+v, %v", decks, err)
	}
	if decks[0].TotalCopies != 1 {
		t.Errorf("deck holds %d copies, want the original 1 — the dry run replaced it", decks[0].TotalCopies)
	}

	// stdin is not a terminal under `go test`, so confirm declines: the live
	// path is unchanged by the dry-run branch beside it.
	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", again}, false); err == nil ||
		!strings.Contains(err.Error(), "already imported") {
		t.Errorf("live re-import: err = %v, want the already-imported refusal", err)
	}
}

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
