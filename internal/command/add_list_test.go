package command

import (
	"context"
	"errors"
	"github.com/spiffcs/hoard/internal/finish"
	"io"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
)

func TestAddListIntoBinder(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	list := []byte("2 Sol Ring (C21) 125\n1x Sol Ring *F*\n")
	if err := addList(ctx, st, bufEnv(io.Discard), list, "paste", "Trade", false); err != nil {
		t.Fatalf("addList: %v", err)
	}
	rows, err := st.BinderByFinish(mustBinder(t, st, "Trade"))
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Finish.String()] += r.Quantity
	}

	if got["nonfoil"] != 2 || got["foil"] != 2 {
		t.Errorf("Trade holds %v, want 2 nonfoil / 2 foil", got)
	}

	if err := addList(ctx, st, bufEnv(io.Discard), list, "paste", "Trade", false); err == nil ||
		!strings.Contains(err.Error(), "--again") {
		t.Errorf("re-paste = %v, want the ledger refusal", err)
	}
	if err := addList(ctx, st, bufEnv(io.Discard), list, "paste", "Trade", true); err != nil {
		t.Errorf("addList --again: %v", err)
	}
}

func TestAddListSkipsAndReportsPartial(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	list := []byte("1 Sol Ring\ngibberish line here\n3 No Such Card\n")
	err := addList(ctx, st, bufEnv(io.Discard), list, "paste", "", false)
	if !errors.Is(err, errPartial) {
		t.Fatalf("messy paste = %v, want errPartial", err)
	}
	rows, _ := st.ListCollectionByFinish()
	found := false
	for _, r := range rows {
		if r.Name == "Sol Ring" && r.Finish == finish.Nonfoil {
			found = true
		}
	}
	if !found {
		t.Error("the resolvable line was not added despite the partial exit")
	}
}

func TestAddListNothingReadable(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	err := addList(context.Background(), st, bufEnv(io.Discard), []byte("total nonsense\n"), "paste", "", false)
	if err == nil || errors.Is(err, errPartial) {
		t.Errorf("unreadable paste = %v, want a plain error", err)
	}
}

func mustBinder(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	b, err := st.BinderByRef(name)
	if err != nil {
		t.Fatalf("BinderByRef(%s): %v", name, err)
	}
	return b.ID
}

func TestStoreAdderKeepsTheGuessAudit(t *testing.T) {
	st := exportStore(t)
	add := storeAdder(st)
	card := scryfall.Card{ID: "brainsurge-id", Set: "mh3", CollectorNumber: "399",
		Name: "Brainsurge", ScryfallURL: "http://x"}

	if err := add(tui.Result{Card: card, Finish: finish.Nonfoil, Qty: 1,
		FinishGuessed: true}); err != nil {
		t.Fatalf("guessed add: %v", err)
	}
	rows, err := st.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Brainsurge" {
		t.Fatalf("guesses = %+v, want the blind commit banked", rows)
	}

	if err := add(tui.Result{Card: card, Finish: finish.Foil, Qty: 1,
		ReplacesFinish: finish.Nonfoil}); err != nil {
		t.Fatalf("correction: %v", err)
	}
	rows, err = st.GuessedFinishes()
	if err != nil {
		t.Fatalf("GuessedFinishes: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("guesses after correction = %+v, want none", rows)
	}

	if err := add(tui.Result{Card: card, Finish: finish.Foil, Qty: 1}); err != nil {
		t.Fatalf("evidenced add: %v", err)
	}
	if rows, _ := st.GuessedFinishes(); len(rows) != 0 {
		t.Errorf("guesses after evidenced add = %+v, want none", rows)
	}
}

func TestCmdAddRefusesPerCopyFlagsOffTheURLForm(t *testing.T) {
	list := deckFile(t, "list.txt", "1 Sol Ring\n")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"list --qty", []string{"add", "--file", list, "--qty", "4"}, "--qty"},
		{"list --foil", []string{"add", "--file", list, "--foil"}, "--foil"},
		{"list both", []string{"add", "--file", list, "--qty", "4", "--foil"}, "--qty"},

		{"list --qty 1", []string{"add", "--file", list, "--qty", "1"}, "--qty"},

		{"implicit pipe", []string{"add", "--qty", "4"}, "--qty"},

		{"picker --qty", []string{"add", "Sol", "Ring", "--qty", "4"}, "--qty"},
		{"picker --foil", []string{"add", "Sol", "Ring", "--foil"}, "--foil"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := exportStore(t)
			stubFetch(t, watchCard())
			_, err := execCmd(context.Background(), st, c.args, false)
			if err == nil {
				t.Fatalf("hoard %v succeeded, want a usage error", c.args)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to name %s", err, c.want)
			}
			totals, _ := st.CollectionTotals()
			if totals.TotalCopies != 3 {
				t.Errorf("collection holds %d copies, want the fixture's 3 untouched",
					totals.TotalCopies)
			}
		})
	}
}

func TestCmdAddListWithoutPerCopyFlagsStillAdds(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	list := deckFile(t, "list.txt", "1 Sol Ring\n")
	if _, err := execCmd(context.Background(), st, []string{"add", "--file", list}, false); err != nil {
		t.Fatalf("hoard add --file: %v", err)
	}
	totals, _ := st.CollectionTotals()
	if totals.TotalCopies != 4 {
		t.Errorf("collection holds %d copies, want the fixture's 3 plus one", totals.TotalCopies)
	}
}
