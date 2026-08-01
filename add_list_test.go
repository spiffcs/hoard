package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

// The paste path end to end: lines resolve through the shared pipeline, land
// in the named binder as one transaction, and the ledger refuses the same
// bytes twice.
func TestAddListIntoBinder(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	list := []byte("2 Sol Ring (C21) 125\n1x Sol Ring *F*\n")
	if err := addList(ctx, st, list, "paste", "Trade", false); err != nil {
		t.Fatalf("addList: %v", err)
	}
	rows, err := st.BinderByFinish(mustBinder(t, st, "Trade"))
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Finish] += r.Quantity
	}
	// The fixture binder already held 1 foil Sol Ring; the paste adds 2
	// non-foil and 1 more foil.
	if got["nonfoil"] != 2 || got["foil"] != 2 {
		t.Errorf("Trade holds %v, want 2 nonfoil / 2 foil", got)
	}

	// The same bytes again are the ledger's case, --again the override.
	if err := addList(ctx, st, list, "paste", "Trade", false); err == nil ||
		!strings.Contains(err.Error(), "--again") {
		t.Errorf("re-paste = %v, want the ledger refusal", err)
	}
	if err := addList(ctx, st, list, "paste", "Trade", true); err != nil {
		t.Errorf("addList --again: %v", err)
	}
}

// A messy paste adds what it can and exits partial: unreadable lines and
// unresolvable cards are reported, not fatal, and not silent.
func TestAddListSkipsAndReportsPartial(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	ctx := context.Background()

	list := []byte("1 Sol Ring\ngibberish line here\n3 No Such Card\n")
	err := addList(ctx, st, list, "paste", "", false)
	if !errors.Is(err, errPartial) {
		t.Fatalf("messy paste = %v, want errPartial", err)
	}
	rows, _ := st.ListCollectionByFinish()
	found := false
	for _, r := range rows {
		if r.Name == "Sol Ring" && r.Finish == "nonfoil" {
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
	err := addList(context.Background(), st, []byte("total nonsense\n"), "paste", "", false)
	if err == nil || errors.Is(err, errPartial) {
		t.Errorf("unreadable paste = %v, want a plain error", err)
	}
}

// mustBinder resolves a binder id by name for assertions.
func mustBinder(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	b, err := st.BinderByRef(name)
	if err != nil {
		t.Fatalf("BinderByRef(%s): %v", name, err)
	}
	return b.ID
}
