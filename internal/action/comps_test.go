package action

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

func TestCompsCacheReloadsOnlyWhenTheDataChanges(t *testing.T) {
	var loads int
	version := int64(7)

	c := &CompsCache{
		owned: func() ([]store.OwnedFinish, error) {
			loads++
			return []store.OwnedFinish{{ScryfallID: "a"}, {ScryfallID: "b"}}, nil
		},
		ver: func() (int64, error) { return version, nil },
		comps: func([]store.OwnedFinish, string) (map[finish.Finish]market.Comp, bool, error) {
			return nil, false, nil
		},
	}

	for range 25 {
		if _, _, err := c.Comps("a"); err != nil {
			t.Fatalf("Comps: %v", err)
		}
	}
	if loads != 1 {
		t.Errorf("%d collection rollups for 25 detail opens, want 1 — the whole "+
			"point is not to re-roll 162k entries per card", loads)
	}

	version = 8
	if _, _, err := c.Comps("b"); err != nil {
		t.Fatalf("Comps after edit: %v", err)
	}
	if loads != 2 {
		t.Errorf("%d rollups after the data version changed, want 2 — a stale "+
			"cache would show comps from before the edit", loads)
	}
}

func TestCompsCacheSeesEveryOwnedRow(t *testing.T) {
	var got []store.OwnedFinish
	c := &CompsCache{
		owned: func() ([]store.OwnedFinish, error) {
			return []store.OwnedFinish{{ScryfallID: "a"}, {ScryfallID: "b"}}, nil
		},
		ver: func() (int64, error) { return 1, nil },
		comps: func(owned []store.OwnedFinish, _ string) (map[finish.Finish]market.Comp, bool, error) {
			got = owned
			return nil, false, nil
		},
	}
	if _, _, err := c.Comps("a"); err != nil {
		t.Fatalf("Comps: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("comps saw %d owned rows, want all 2 — narrowing the set would "+
			"change the cached-quotes hit semantics", len(got))
	}
}
