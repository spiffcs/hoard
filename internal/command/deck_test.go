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

func deckFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

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

func TestDeckAddDryRunWritesNothing(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	list := deckFile(t, "Fish Tank.txt", "2 Sol Ring (c21) 125\n1 Mystic Remora\n")

	out, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list, "--dry-run"}, false)
	if err != nil {
		t.Fatalf("hoard deck add --dry-run: %v", err)
	}

	if !strings.Contains(out, `Would import deck "Fish Tank" (text): 2 cards resolved.`) {
		t.Errorf("dry run headline missing or malformed:\n%s", out)
	}
	if strings.Contains(out, "#") {
		t.Errorf("dry run printed a deck id:\n%s", out)
	}

	if !strings.Contains(out, "Dry run: nothing was written.") {
		t.Errorf("dry run did not say nothing was written:\n%s", out)
	}
	assertNothingWritten(t, st, "dry run")

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

	for _, want := range []string{
		"1 card could not be resolved and was skipped:",
		"Card That Does Not Exist",
		"1 line could not be read and was skipped:",
		"~~~ not a decklist line ~~~",
		"Dry run: nothing was written.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output missing %q; got:\n%s", want, out)
		}
	}

	assertNothingWritten(t, st, "partial dry run")
}

func TestDeckAddDryRunOverAnImportedDeckReportsRatherThanAsks(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	list := deckFile(t, "Stompy.txt", "1 Mystic Remora\n")

	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", list}, false); err != nil {
		t.Fatalf("first import: %v", err)
	}

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

	if _, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", again}, false); err == nil ||
		!strings.Contains(err.Error(), "already imported") {
		t.Errorf("live re-import: err = %v, want the already-imported refusal", err)
	}
}

func TestDeckAddResolvesOfflineWithNameRetry(t *testing.T) {
	st := importStore(t)
	calls := stubFetch(t, importFixtures()...)

	list := filepath.Join(t.TempDir(), "Fish Tank.txt")

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

	decks, _ := st.ListDecks()
	if len(decks) != 1 || decks[0].TotalCopies != 1 {
		t.Errorf("decks = %+v, want the remora imported despite the ghost", decks)
	}
}

func TestDeckAddStdin(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	withStdin(t, "2 Sol Ring (c21) 125\n1 Mystic Remora\n")

	if _, err := execCmd(context.Background(), st,
		[]string{"deck", "add", "--file", "-", "--name", "Fish Tank"}, false); err != nil {
		t.Fatalf("hoard deck add --file -: %v", err)
	}
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].Name != "Fish Tank" || decks[0].TotalCopies != 3 {
		t.Errorf("decks = %+v, want one Fish Tank holding 3", decks)
	}
}

func TestDeckAddStdinNeedsAName(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	withStdin(t, "2 Sol Ring (c21) 125\n")

	_, err := execCmd(context.Background(), st, []string{"deck", "add", "--file", "-"}, false)
	if err == nil {
		t.Fatal("hoard deck add --file - with no --name succeeded, want a usage error")
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("err = %v, want it to name --name", err)
	}
	assertNothingWritten(t, st, "deck add --file - with no --name")
}
