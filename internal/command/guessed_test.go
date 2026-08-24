package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func guessedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	card := scryfall.Card{ID: "whisperer", Set: "lgn", CollectorNumber: "135",
		Name: "Primal Whisperer", ScryfallURL: "http://x"}
	if err := st.AddCardFinish(card, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	for range 2 {
		if err := st.RecordFinishGuess(0, card.ID, finish.Nonfoil); err != nil {
			t.Fatalf("RecordFinishGuess: %v", err)
		}
	}
	return st
}

func splitEnv() (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	e := ui.Env{Width: 80}
	return &cli.Env{Out: out, Err: errOut, OutEnv: e, ErrEnv: e}, out, errOut
}

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

	left := guessIDs(t, st)
	if len(left) != 1 || left[0] != ids[1] {
		t.Errorf("ids after --checked = %v, want just %d", left, ids[1])
	}
}

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

	env, listed, _ := splitEnv()
	if err := runGuessed(st, env); err != nil {
		t.Fatalf("runGuessed: %v", err)
	}
	if !strings.Contains(listed.String(), "has been checked") {
		t.Errorf("empty listing = %q, want the checked-clean sentence", listed.String())
	}
}

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

func TestGuessedListingReadsAsAWorklist(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)

	env, out, _ := splitEnv()
	if err := runGuessed(st, env); err != nil {
		t.Fatalf("runGuessed: %v", err)
	}
	got := out.String()

	for _, want := range []string{

		"2 scanned rows committed without finish evidence:",
		"Primal Whisperer (LGN/135) nonfoil · guessed ",

		"#" + itoa(ids[0]),
		"#" + itoa(ids[1]),

		"confirm a right one with hoard guessed --checked <id>",

		"Fix a wrong one in browse (enter → finish)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing is missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "which clears it here.") {
		t.Errorf("listing still names only the correction path:\n%s", got)
	}
}

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

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestGuessedJSONCarriesTheIDsCheckedTakes(t *testing.T) {
	st := guessedStore(t)

	out, err := execCmd(context.Background(), st, []string{"guessed"}, true)
	if err != nil {
		t.Fatalf("guessed --json: %v", err)
	}
	var doc struct {
		Kind    string `json:"kind"`
		Guessed struct {
			Rows []struct {
				ID   int64 `json:"id"`
				Card struct {
					Name   string `json:"name"`
					Finish string `json:"finish"`
				} `json:"card"`
				GuessedAt string `json:"guessedAt"`
			} `json:"rows"`
		} `json:"guessed"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("guessed --json emitted invalid JSON (%v): %q", err, out)
	}
	if doc.Kind != "guessed" {
		t.Errorf("kind = %q, want %q", doc.Kind, "guessed")
	}

	if len(doc.Guessed.Rows) != 2 {
		t.Fatalf("rows = %d, want the two banked guesses: %s", len(doc.Guessed.Rows), out)
	}
	r := doc.Guessed.Rows[0]
	if r.Card.Name != "Primal Whisperer" || r.Card.Finish != "nonfoil" || r.GuessedAt == "" {
		t.Errorf("row is missing what the table shows: %+v", r)
	}

	env, _, _ := splitEnv()
	if err := runGuessedChecked(st, env, []int64{r.ID}); err != nil {
		t.Fatalf("runGuessedChecked(%d): %v", r.ID, err)
	}
	if left := guessIDs(t, st); len(left) != 1 {
		t.Errorf("ids after retiring the one the document named = %v, want one left", left)
	}
}

func TestGuessedJSONOnAnEmptyQueueIsAnEmptyList(t *testing.T) {
	st := guessedStore(t)
	env, _, _ := splitEnv()
	if err := runGuessedChecked(st, env, guessIDs(t, st)); err != nil {
		t.Fatalf("runGuessedChecked: %v", err)
	}

	out, err := execCmd(context.Background(), st, []string{"guessed"}, true)
	if err != nil {
		t.Fatalf("guessed --json: %v", err)
	}
	if !strings.Contains(out, `"rows": []`) {
		t.Errorf("a drained queue does not emit an empty list:\n%s", out)
	}
}

func TestGuessedCheckedRefusesJSON(t *testing.T) {
	st := guessedStore(t)
	ids := guessIDs(t, st)

	_, err := execCmd(context.Background(), st,
		[]string{"guessed", "--checked", itoa(ids[0])}, true)
	if err == nil {
		t.Fatal("guessed --checked --json succeeded; it has no document to emit")
	}
	if !errors.Is(err, cli.ErrUsage) {
		t.Errorf("refusal is not a usage error: %v", err)
	}
	if !strings.Contains(err.Error(), "hoard guessed --json") {
		t.Errorf("message does not name the command that does emit one: %v", err)
	}

	if left := guessIDs(t, st); len(left) != 2 {
		t.Errorf("ids after the refusal = %v, want both still queued", left)
	}
}
