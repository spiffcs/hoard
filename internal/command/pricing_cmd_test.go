package command

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// pricedStore holds two priced cards and nothing unpriced, so a refresh
// touches no gap-fill network path.
func pricedStore(t *testing.T) *store.Store {
	t.Helper()
	st := exportStore(t)
	// exportStore's Mystic Remora is unpriced; price it so FillGaps stays
	// offline (a gap would send the test to MTGJSON).
	rem := scryfall.Card{ID: "rem", Set: "ice", CollectorNumber: "78",
		Name: "Mystic Remora", ScryfallURL: "http://y", PriceUSD: f(4)}
	if err := st.UpsertPrintings([]scryfall.Card{rem}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	return st
}

// The stdout lock for update-prices: these bytes are the piped contract, and
// the action migration must never change them.
func TestRunUpdatePricesStdoutGolden(t *testing.T) {
	st := pricedStore(t)
	stubFetch(t, watchCard(), scryfall.Card{ID: "rem", Set: "ice", CollectorNumber: "78",
		Name: "Mystic Remora", ScryfallURL: "http://y", PriceUSD: f(4)})

	var sb strings.Builder
	deps := action.Deps{Store: st, Resolver: cardResolver}
	if err := runUpdatePrices(context.Background(), deps, bufEnv(&sb), 10); err != nil {
		t.Fatalf("runUpdatePrices: %v", err)
	}
	want := "Updated prices for 2 of 2 cards.\n\nNo price changes since the last refresh.\n"
	if sb.String() != want {
		t.Errorf("stdout:\n%q\nwant:\n%q", sb.String(), want)
	}
}

func TestRunUpdatePricesEmptyStore(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/empty.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	var sb strings.Builder
	if err := runUpdatePrices(context.Background(), action.Deps{Store: st}, bufEnv(&sb), 10); err != nil {
		t.Fatalf("runUpdatePrices: %v", err)
	}
	if sb.String() != "No cards yet; nothing to update.\n" {
		t.Errorf("stdout = %q", sb.String())
	}
}
