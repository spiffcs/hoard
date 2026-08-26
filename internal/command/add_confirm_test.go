package command

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
)

const solConfirmDocument = `{"id": "sol-id-1", "name": "Sol Ring", "set": "c21",
	"set_name": "Commander 2021", "collector_number": "125", "rarity": "uncommon",
	"lang": "en", "type_line": "Artifact", "oracle_text": "{T}: Add {C}{C}.",
	"released_at": "2021-04-23", "color_identity": [], "finishes": ["nonfoil", "foil"],
	"prices": {"usd": "3.50", "usd_foil": "9.00"}}`

func stubPriceCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := priceCacheDir
	t.Cleanup(func() { priceCacheDir = old })
	priceCacheDir = func() string { return dir }
}

func stubCardRoute(t *testing.T, card *scryfall.Card, err error) *int {
	t.Helper()
	calls := new(int)
	old := cardResolver.Card
	t.Cleanup(func() { cardResolver.Card = old })
	cardResolver.Card = func(_ context.Context, _, _, _ string) (*scryfall.Card, error) {
		*calls++
		return card, err
	}
	return calls
}

func catalogSolRing() scryfall.Card {
	return scryfall.Card{ID: "sol-id-1", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "125", Lang: "en", PriceUSD: fp(1.50),
		Finishes: []string{"nonfoil", "foil"}}
}

func fp(v float64) *float64 { return &v }

func confirmStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	stubPriceCache(t)
	binder, err := st.CreateBinder("Trade")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	return st, binder
}

func TestAddSessionConfirmLeavesTheCardWholeInItsBinder(t *testing.T) {
	st, binder := confirmStore(t)
	bulk := stubFetch(t)
	doc := scryfall.Card{ID: "sol-id-1", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "125", SetName: "Commander 2021", Lang: "en",
		PriceUSD: fp(3.50), PriceUSDFoil: fp(9.00),
		Finishes: []string{"nonfoil", "foil"}, Raw: []byte(solConfirmDocument)}
	route := stubCardRoute(t, &doc, nil)

	res := tui.Result{Card: catalogSolRing(), Finish: finish.Nonfoil,
		Qty: 1, ContainerID: binder}
	if err := storeAdder(st)(res); err != nil {
		t.Fatalf("confirming the add: %v", err)
	}
	if err := storeCompleter(context.Background(), st)(res); err != nil {
		t.Fatalf("completing the add: %v", err)
	}

	rows, err := st.BinderByFinish(binder)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 1 {
		t.Fatalf("binder = %+v, want the one card the session confirmed", rows)
	}

	d, err := st.CardDetail("sol-id-1")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if !d.Enriched {
		t.Error("the card left the session with no stored document; browse hides it " +
			"behind every trait filter until update-prices runs")
	}
	if d.TypeLine != "Artifact" || d.Rarity != "uncommon" {
		t.Errorf("detail = type %q rarity %q, want the fetched document's", d.TypeLine, d.Rarity)
	}

	series, err := st.PriceSeries("sol-id-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) == 0 {
		t.Error("the card left the session with no price point")
	}

	if *route != 1 {
		t.Errorf("individual card route called %d times, want once per confirmed card", *route)
	}
	if *bulk != 0 {
		t.Errorf("bulk collection route called %d times, want never", *bulk)
	}
}

func TestAddSessionConfirmSurvivesAnOutage(t *testing.T) {
	st, binder := confirmStore(t)
	stubFetch(t)
	stubCardRoute(t, nil, context.DeadlineExceeded)

	res := tui.Result{Card: catalogSolRing(), Finish: finish.Nonfoil,
		Qty: 2, ContainerID: binder}
	if err := storeAdder(st)(res); err != nil {
		t.Fatalf("an outage must not lose the add: %v", err)
	}
	if err := storeCompleter(context.Background(), st)(res); err == nil {
		t.Error("the outage was not reported back to the session")
	}

	rows, err := st.BinderByFinish(binder)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 2 {
		t.Errorf("binder = %+v, want both copies kept", rows)
	}
}

func TestAddSessionReportsCardsItCouldNotComplete(t *testing.T) {
	st, binder := confirmStore(t)
	stubFetch(t)
	stubCardRoute(t, nil, errors.New("scryfall is down"))

	res := tui.Result{Card: catalogSolRing(), Finish: finish.Nonfoil,
		Qty: 1, ContainerID: binder}
	if err := storeAdder(st)(res); err != nil {
		t.Fatalf("the add itself must still succeed: %v", err)
	}

	failing := storeCompleter(context.Background(), st)
	if err := failing(tui.Result{Card: scryfall.Card{ID: "ghost", Name: "Ghost",
		Set: "zzz", CollectorNumber: "1"}, Finish: finish.Nonfoil}); err == nil {
		t.Error("a completion that could not run reported success; " +
			"the session has nothing to tell the user about")
	}

	rows, err := st.BinderByFinish(binder)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("binder = %+v, want the card kept regardless", rows)
	}
}

func TestAddSessionSaysNothingWhenEveryCardCompletes(t *testing.T) {
	st, binder := confirmStore(t)
	stubFetch(t)
	doc := scryfall.Card{ID: "sol-id-1", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "125", Lang: "en", PriceUSD: fp(3.50),
		Finishes: []string{"nonfoil", "foil"}, Raw: []byte(solConfirmDocument)}
	stubCardRoute(t, &doc, nil)

	res := tui.Result{Card: catalogSolRing(), Finish: finish.Nonfoil,
		Qty: 1, ContainerID: binder}
	if err := storeAdder(st)(res); err != nil {
		t.Fatalf("confirming the add: %v", err)
	}
	if err := storeCompleter(context.Background(), st)(res); err != nil {
		t.Errorf("a clean run reported %v", err)
	}
}

func TestIncompleteAddsLineNamesTheCardsAndTheRemedy(t *testing.T) {
	line := incompleteAddsLine([]string{"Sol Ring (C21/125)", "Mystic Remora (ICE/78)"})
	for _, want := range []string{"Sol Ring (C21/125)", "Mystic Remora (ICE/78)", "update-prices"} {
		if !strings.Contains(line, want) {
			t.Errorf("line = %q, want it to mention %q", line, want)
		}
	}
	if incompleteAddsLine(nil) != "" {
		t.Errorf("line for no failures = %q, want nothing said",
			incompleteAddsLine(nil))
	}
}
