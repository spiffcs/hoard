package action

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func TestEnsureCatalogDeclinedIsNotUsable(t *testing.T) {
	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()

	var notes []string
	p := func(ev progress.Event) {
		if ev.Note != "" {
			notes = append(notes, ev.Note)
		}
	}

	d := Deps{Catalog: cat, Confirm: func(string) bool { return false }}
	if EnsureCatalog(context.Background(), d, p) {
		t.Error("an empty catalog was reported as usable for prices")
	}
	if len(notes) != 1 {
		t.Errorf("notes = %v, want the fall-through-to-API narration", notes)
	}

	if EnsureCatalog(context.Background(), Deps{Catalog: cat}, nil) {
		t.Error("nil Confirm accepted a download")
	}
}

func TestEnsureCatalogNilCatalog(t *testing.T) {
	if EnsureCatalog(context.Background(), Deps{}, nil) {
		t.Error("a nil catalog was reported as usable")
	}
}

func TestCatalogStatusNilCatalog(t *testing.T) {
	if _, err := CatalogStatus(context.Background(), Deps{}); err == nil {
		t.Error("CatalogStatus with no catalog succeeded")
	}
	if _, err := CatalogUpdate(context.Background(), Deps{}, nil); err == nil {
		t.Error("CatalogUpdate with no catalog succeeded")
	}
}

func TestUpdatePricesDropsUnusableCatalog(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	sol := scryfall.Card{ID: "sol", Set: "c21", CollectorNumber: "125",
		Name: "Sol Ring", ScryfallURL: "http://x", PriceUSD: f(2)}
	tomb := scryfall.Card{ID: "tomb", Set: "uma", CollectorNumber: "236",
		Name: "Ancient Tomb", ScryfallURL: "http://z", PriceUSD: f(30)}
	for _, c := range []scryfall.Card{sol, tomb} {
		if err := st.AddCardFinish(c, finish.Nonfoil, 1); err != nil {
			t.Fatalf("AddCardFinish: %v", err)
		}
	}

	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
	}
	defer cat.Close()

	var fetched []scryfall.Identifier
	deps := Deps{
		Store:   st,
		Catalog: cat,
		Confirm: func(string) bool { return false },
		Resolver: &resolve.Resolver{Fetch: func(_ context.Context,
			ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
			fetched = ids
			return []scryfall.Card{sol, tomb}, nil, nil
		}},
	}
	res, err := UpdatePrices(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if res.CatalogUsed {
		t.Error("an empty catalog with a declined download was reported as used")
	}
	if res.FromCatalog != 0 {
		t.Errorf("FromCatalog = %d, want 0 — no price may come from an unusable catalog", res.FromCatalog)
	}
	if len(fetched) != 2 {
		t.Errorf("live fetch got %d identifiers, want every printing (2)", len(fetched))
	}
	if res.Found != 2 || res.Total != 2 {
		t.Errorf("result = %+v, want 2 of 2 found", res)
	}
}

func f(v float64) *float64 { return &v }
