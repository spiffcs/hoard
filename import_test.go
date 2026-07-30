package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// stubFetch swaps the Scryfall bulk lookup for an in-memory one over the given
// cards, matching by the same keys resolveIDs uses. Returns a call counter so
// tests can see whether the name-fallback pass ran.
func stubFetch(t *testing.T, cards ...scryfall.Card) *int {
	t.Helper()
	index := resolveIDs(cards)
	byID := make(map[string]scryfall.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	calls := new(int)
	old := fetchCollection
	t.Cleanup(func() { fetchCollection = old })
	fetchCollection = func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
		*calls++
		var found []scryfall.Card
		var notFound []scryfall.Identifier
		seen := make(map[string]bool)
		for _, ident := range ids {
			id, ok := index[ident.Key()]
			if !ok {
				notFound = append(notFound, ident)
				continue
			}
			if !seen[id] {
				seen[id] = true
				found = append(found, byID[id])
			}
		}
		return found, notFound, nil
	}
	return calls
}

func importStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Fully priced printings, so fillPriceGaps finds no gap and never talks to
// MTGJSON in tests.
func importFixtures() []scryfall.Card {
	return []scryfall.Card{
		{ID: "sol-id-1", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
			ScryfallURL: "http://sol", PriceUSD: f(2), PriceUSDFoil: f(12.5),
			Finishes: []string{"nonfoil", "foil"}},
		{ID: "bolt-id-1", Set: "2x2", CollectorNumber: "117", Name: "Lightning Bolt",
			ScryfallURL: "http://bolt", PriceUSD: f(1.5), PriceUSDFoil: f(4),
			Finishes: []string{"nonfoil", "foil"}},
		{ID: "remora-id-1", Set: "ice", CollectorNumber: "78", Name: "Mystic Remora",
			ScryfallURL: "http://rem", PriceUSD: f(5), PriceUSDFoil: f(50),
			Finishes: []string{"nonfoil"}},
	}
}

// The ManaBox fixture: 2 Sol Ring + 1 foil Bolt in "Trade Binder", 1 Remora
// with no binder name. Preserving binders must fan them out accordingly.
func TestCmdImportPreservesManaBoxBinders(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	err := cmdImport(context.Background(), st,
		[]string{"--preserve-binders", "internal/collsource/testdata/manabox.csv"})
	if err != nil {
		t.Fatalf("cmdImport: %v", err)
	}

	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if len(binders) != 2 || binders[1].Name != "Trade Binder" {
		t.Fatalf("binders = %+v, want the default plus Trade Binder", binders)
	}
	if got := binders[1].TotalCopies; got != 3 {
		t.Errorf("Trade Binder holds %d copies, want 3", got)
	}
	rows, err := st.BinderByFinish(binders[0].ID)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Mystic Remora" || rows[0].Quantity != 1 {
		t.Errorf("default binder rows = %+v, want just the remora", rows)
	}
}

// A set+number Scryfall does not know still resolves through the second,
// name-keyed pass rather than being reported unresolved.
func TestCmdImportFallsBackToNames(t *testing.T) {
	st := importStore(t)
	calls := stubFetch(t, importFixtures()...)
	csv := filepath.Join(t.TempDir(), "mox.csv")
	os.WriteFile(csv, []byte(
		"Count,Tradelist Count,Name,Edition,Collector Number,Foil\n"+
			"1,0,Sol Ring,zzz,999,\n"), 0o644)

	if err := cmdImport(context.Background(), st, []string{csv}); err != nil {
		t.Fatalf("cmdImport: %v", err)
	}
	if *calls != 2 {
		t.Errorf("fetch called %d times, want 2 (bulk pass + name fallback)", *calls)
	}
	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 1 {
		t.Errorf("copies = %d, want the fallback-resolved Sol Ring", totals.TotalCopies)
	}
}

func TestCmdImportDryRunWritesNothing(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	err := cmdImport(context.Background(), st,
		[]string{"--dry-run", "--preserve-binders", "internal/collsource/testdata/manabox.csv"})
	if err != nil {
		t.Fatalf("cmdImport: %v", err)
	}
	totals, _ := st.CollectionTotals()
	if totals.TotalCopies != 0 {
		t.Errorf("dry run wrote %d copies", totals.TotalCopies)
	}
	binders, _ := st.ListBinders()
	if len(binders) != 1 {
		t.Errorf("dry run created binders: %+v", binders)
	}
}

// The sprint checkpoint: export → import into a fresh database → identical
// totals, with the binder structure intact.
func TestExportImportRoundTrip(t *testing.T) {
	src := importStore(t)
	cards := importFixtures()
	if err := src.AddCardFinish(cards[0], "normal", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	trade, err := src.CreateBinder("Trade")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[0], "foil", 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[2], "normal", 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}

	file := filepath.Join(t.TempDir(), "roundtrip.csv")
	if err := cmdExport(src, []string{"-o", file}); err != nil {
		t.Fatalf("cmdExport: %v", err)
	}

	dst := importStore(t)
	stubFetch(t, cards...)
	if err := cmdImport(context.Background(), dst, []string{"--preserve-binders", file}); err != nil {
		t.Fatalf("cmdImport: %v", err)
	}

	want, err := src.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	got, err := dst.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if got != want {
		t.Errorf("round-trip totals = %+v, want %+v", got, want)
	}
	srcBinders, _ := src.ListBinders()
	dstBinders, _ := dst.ListBinders()
	if len(srcBinders) != len(dstBinders) {
		t.Fatalf("binders: %d vs %d", len(srcBinders), len(dstBinders))
	}
	for i := range srcBinders {
		if srcBinders[i].Name != dstBinders[i].Name || srcBinders[i].TotalCopies != dstBinders[i].TotalCopies {
			t.Errorf("binder %d: %s/%d vs %s/%d", i,
				srcBinders[i].Name, srcBinders[i].TotalCopies,
				dstBinders[i].Name, dstBinders[i].TotalCopies)
		}
	}
}

func TestCmdImportRejectsBadFlagCombos(t *testing.T) {
	st := importStore(t)
	for _, args := range [][]string{
		{},
		{"a.csv", "b.csv"},
		{"--binder", "Trade", "--preserve-binders", "a.csv"},
	} {
		if err := cmdImport(context.Background(), st, args); err == nil {
			t.Errorf("cmdImport(%v) succeeded, want an error", args)
		}
	}
}
