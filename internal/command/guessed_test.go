package command

// `hoard guessed` is a worklist, not a ledger: every row in it is a card
// somebody still has to pick up and look at. These pin that it can be emptied.

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// guessedStore holds two copies of one printing, both committed on the
// scanner's default. Two copies rather than one because that is the case the
// natural key cannot address: the log is per commit, so the two rows are
// identical in every column a listing prints.
func guessedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	card := scryfall.Card{ID: "whisperer", Set: "lgn", CollectorNumber: "135",
		Name: "Primal Whisperer", ScryfallURL: "http://x"}
	if err := st.AddCardFinish(card, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	// 0 is the default-binder sentinel the adder passes; the store resolves it.
	for range 2 {
		if err := st.RecordFinishGuess(0, card.ID, "nonfoil"); err != nil {
			t.Fatalf("RecordFinishGuess: %v", err)
		}
	}
	return st
}

// splitEnv keeps stdout and stderr apart, because a partial outcome here is a
// warning on Err and the count of what was retired is the answer on Out.
func splitEnv() (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	e := ui.Env{Width: 80}
	return &cli.Env{Out: out, Err: errOut, OutEnv: e, ErrEnv: e}, out, errOut
}

// guessIDs is the ids currently standing, newest first.
func guessIDs(t *testing.T, st *store.Store) []int64 {
	t.Helper()
	rows, err := st.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// THE DECISION. `guessed` is a worklist -- store.MoveEntryFinish says so
// outright, retiring a guess in the same transaction as a correction so that
// "hoard guessed drains as the pile gets checked" -- and a worklist that can
// only be answered by a correction cannot drain, because a scan that guessed
// right is never re-keyed. This is the missing half: the row leaves when the
// card is checked and the guess turns out to have been correct.
//
// Before --checked existed there was no way to write this test at all, which
// is the defect stated as precisely as it can be.
func TestGuessedCheckedRetiresTheRow(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want two guesses banked", ids)
	}

	env, out, _ := splitEnv()
	if err := runGuessedChecked(st, env, []int64{ids[0]}); err != nil {
		t.Fatalf("runGuessedChecked: %v", err)
	}
	if got, want := out.String(), "Retired 1 guess: the finish was right as scanned.\n"; got != want {
		t.Errorf("report = %q, want %q", got, want)
	}

	// The entry actually left, and took nothing else with it: the other copy
	// is a separate card that still needs looking at.
	left := guessIDs(t, st)
	if len(left) != 1 || left[0] != ids[1] {
		t.Errorf("ids after --checked = %v, want just %d", left, ids[1])
	}
}

// The queue reaches zero. That is the whole claim: an append-only log cannot,
// and the count of a list that cannot means nothing.
func TestGuessedQueueCanReachZero(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)

	env, out, _ := splitEnv()
	if err := runGuessedChecked(st, env, ids); err != nil {
		t.Fatalf("runGuessedChecked: %v", err)
	}
	if got, want := out.String(), "Retired 2 guesses: the finish was right as scanned.\n"; got != want {
		t.Errorf("report = %q, want %q", got, want)
	}
	if left := guessIDs(t, st); len(left) != 0 {
		t.Fatalf("ids after checking every card = %v, want none", left)
	}

	// And the empty state's promise -- "evidence-backed or has been checked"
	// -- is now something the tool can actually deliver.
	env, listed, _ := splitEnv()
	if err := runGuessed(st, env); err != nil {
		t.Fatalf("runGuessed: %v", err)
	}
	if !strings.Contains(listed.String(), "has been checked") {
		t.Errorf("empty listing = %q, want the checked-clean sentence", listed.String())
	}
}

// An id off a stale listing is a partial outcome, not a failure: it warns on
// Err, keeps the exit code, and does not claim to have retired anything.
func TestGuessedCheckedWarnsOnAnIDThatNamesNothing(t *testing.T) {
	st := guessedStore(t)

	env, out, errOut := splitEnv()
	if err := runGuessedChecked(st, env, []int64{9999}); err != nil {
		t.Fatalf("runGuessedChecked: %v", err)
	}
	if got, want := out.String(), "Retired 0 guesses: the finish was right as scanned.\n"; got != want {
		t.Errorf("report = %q, want %q", got, want)
	}
	if !strings.Contains(errOut.String(), "No guess #9999") {
		t.Errorf("stderr = %q, want a warning naming the id", errOut.String())
	}
	if left := guessIDs(t, st); len(left) != 2 {
		t.Errorf("ids = %v, want both still standing", left)
	}
}

// The clean case, whole. A row has to carry its id to be addressable, and the
// footer has to name both ways out -- naming only the correction is what left
// the list unable to shrink. Everything else is as it was.
func TestGuessedListingReadsAsAWorklist(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)

	env, out, _ := splitEnv()
	if err := runGuessed(st, env); err != nil {
		t.Fatalf("runGuessed: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		// Unchanged: the headline, and the row's card furniture.
		"2 scanned rows committed without finish evidence:",
		"Primal Whisperer (LGN/135) nonfoil · guessed ",
		// New: an id per row, so a row can be named.
		"#" + itoa(ids[0]),
		"#" + itoa(ids[1]),
		// New: the way out for a guess that was right.
		"confirm a right one with hoard guessed --checked <id>",
		// Unchanged: the way out for a guess that was wrong.
		"Fix a wrong one in browse (enter → finish)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing is missing %q:\n%s", want, got)
		}
	}
	// The old footer promised only the correction path.
	if strings.Contains(got, "which clears it here.") {
		t.Errorf("listing still names only the correction path:\n%s", got)
	}
}

// Through the real tree, so the flag itself is covered: --checked has to parse
// as a repeatable id list, not just as a function argument.
func TestGuessedCheckedFlagDispatches(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)

	got, err := execCmd(context.Background(), st,
		[]string{"guessed", "--checked", itoa(ids[0]), "--checked", itoa(ids[1])}, false)
	if err != nil {
		t.Fatalf("guessed --checked: %v", err)
	}
	if !strings.Contains(got, "Retired 2 guesses") {
		t.Errorf("out = %q, want both retired", got)
	}
	if left := guessIDs(t, st); len(left) != 0 {
		t.Errorf("ids = %v, want none", left)
	}
}

// itoa keeps the id formatting in one place; the listing and the flag have to
// agree on it or the ids a user reads cannot be typed back in.
func itoa(id int64) string { return strconv.FormatInt(id, 10) }
