package action

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const solDocument = `{"id": "sol-id-1", "name": "Sol Ring", "set": "c21",
	"set_name": "Commander 2021", "collector_number": "125", "rarity": "uncommon",
	"lang": "en", "type_line": "Artifact", "oracle_text": "{T}: Add {C}{C}.",
	"released_at": "2021-04-23", "color_identity": [], "finishes": ["nonfoil", "foil"],
	"prices": {"usd": "3.50", "usd_foil": "9.00"}}`

func solDocumentCard() scryfall.Card {
	return scryfall.Card{
		ID: "sol-id-1", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		SetName: "Commander 2021", ReleasedAt: "2021-04-23", Lang: "en",
		Finishes: []string{"nonfoil", "foil"},
		PriceUSD: f(3.50), PriceUSDFoil: f(9.00),
		Raw: []byte(solDocument),
	}
}

func solFromCatalog() scryfall.Card {
	return scryfall.Card{
		ID: "sol-id-1", Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Lang: "en", Finishes: []string{"nonfoil", "foil"}, PriceUSD: f(1.50),
	}
}

type addFixture struct {
	deps     Deps
	store    *store.Store
	cacheDir string
	asked    []string
	bulk     int
}

func newAddFixture(t *testing.T, doc *scryfall.Card, docErr error) *addFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	fx := &addFixture{store: st, cacheDir: t.TempDir()}
	fx.deps = Deps{
		Store:    st,
		CacheDir: fx.cacheDir,
		Resolver: &resolve.Resolver{
			Fetch: func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
				fx.bulk++
				return nil, ids, nil
			},
			Card: func(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
				fx.asked = append(fx.asked, set+"/"+number+"/"+lang)
				return doc, docErr
			},
		},
	}
	return fx
}

func (fx *addFixture) confirm(t *testing.T, c scryfall.Card, fin finish.Finish) {
	t.Helper()
	if err := fx.store.AddCardFinish(c, fin, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
}

func TestCompleteAddStoresTheDocumentFromTheSingleCardRoute(t *testing.T) {
	doc := solDocumentCard()
	fx := newAddFixture(t, &doc, nil)
	fx.confirm(t, solFromCatalog(), finish.Nonfoil)

	if err := CompleteAdd(context.Background(), fx.deps, solFromCatalog(), finish.Nonfoil); err != nil {
		t.Fatalf("CompleteAdd: %v", err)
	}

	d, err := fx.store.CardDetail("sol-id-1")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if !d.Enriched {
		t.Error("the confirmed card still has no stored document; browse will say " +
			"\"card details not stored yet\" and every trait filter will hide it")
	}
	if d.TypeLine != "Artifact" || d.Rarity != "uncommon" || d.SetName != "Commander 2021" {
		t.Errorf("detail = type %q rarity %q set %q, want the fetched document's",
			d.TypeLine, d.Rarity, d.SetName)
	}
	if d.PriceUSD == nil || *d.PriceUSD != 3.50 {
		t.Errorf("price = %s, want 3.50 from the card route, not the catalog's stale 1.50",
			ui.MoneyPtr(d.PriceUSD))
	}
}

func TestCompleteAddUsesTheIndividualRouteAndNotTheBulkOne(t *testing.T) {
	doc := solDocumentCard()
	fx := newAddFixture(t, &doc, nil)
	fx.confirm(t, solFromCatalog(), finish.Nonfoil)

	if err := CompleteAdd(context.Background(), fx.deps, solFromCatalog(), finish.Nonfoil); err != nil {
		t.Fatalf("CompleteAdd: %v", err)
	}

	if fx.bulk != 0 {
		t.Errorf("the bulk collection route was called %d times; one confirmed card "+
			"must go to the individual card route", fx.bulk)
	}
	if len(fx.asked) != 1 || fx.asked[0] != "c21/125/en" {
		t.Errorf("card route asked for %v, want exactly [c21/125/en]", fx.asked)
	}
}

func TestCompleteAddRecordsTodaysPriceForTheNewCard(t *testing.T) {
	doc := solDocumentCard()
	fx := newAddFixture(t, &doc, nil)
	fx.confirm(t, solFromCatalog(), finish.Nonfoil)

	if err := CompleteAdd(context.Background(), fx.deps, solFromCatalog(), finish.Nonfoil); err != nil {
		t.Fatalf("CompleteAdd: %v", err)
	}

	series, err := fx.store.PriceSeries("sol-id-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) == 0 {
		t.Fatal("the card has no price point; its history starts only at the next update-prices")
	}
	if last := series[len(series)-1]; last.Price != 3.50 {
		t.Errorf("recorded price = %v, want the 3.50 the card route quoted", last.Price)
	}
}

func TestCompleteAddBackfillsTheCardsHistoryFromTheCachedArchive(t *testing.T) {
	doc := solDocumentCard()
	fx := newAddFixture(t, &doc, nil)
	fx.confirm(t, solFromCatalog(), finish.Nonfoil)
	writeCachedArchive(t, fx.cacheDir, time.Now().Format("2006-01-02"),
		`{"data": {"uuid-sol": {"paper": {"tcgplayer": {"currency": "USD",
			"retail": {"normal": {"2026-08-20": 2.00, "2026-08-21": 2.10,
			"2026-08-22": 2.25, "2026-08-23": 2.20, "2026-08-24": 2.40}}}}}}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		zw := gzip.NewWriter(w)
		io.WriteString(zw, `{"data": {"cards": [{"uuid": "uuid-sol",
			"identifiers": {"scryfallId": "sol-id-1"}}]}}`)
		zw.Close()
	}))
	defer srv.Close()
	fx.deps.PriceBaseURL = srv.URL

	if err := CompleteAdd(context.Background(), fx.deps, solFromCatalog(), finish.Nonfoil); err != nil {
		t.Fatalf("CompleteAdd: %v", err)
	}

	series, err := fx.store.PriceSeries("sol-id-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) < 5 {
		t.Errorf("sparkline has %d points, want the archive's five days backdated at add time",
			len(series))
	}
}

func TestCompleteAddKeepsTheCardWhenTheCardRouteFails(t *testing.T) {
	fx := newAddFixture(t, nil, errors.New("scryfall is down"))
	fx.confirm(t, solFromCatalog(), finish.Nonfoil)

	if err := CompleteAdd(context.Background(), fx.deps, solFromCatalog(), finish.Nonfoil); err == nil {
		t.Error("an outage was reported as success; the session cannot tell the user " +
			"which cards still need filling in")
	}

	rows, err := fx.store.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].ScryfallID != "sol-id-1" {
		t.Errorf("collection = %+v, want the card the session confirmed", rows)
	}
}

func TestCompleteAddRefusesADocumentForADifferentPrinting(t *testing.T) {
	english := solDocumentCard()
	english.ID = "english-twin"
	fx := newAddFixture(t, &english, nil)

	japanese := solFromCatalog()
	japanese.ID = "jp-id"
	japanese.Lang = "ja"
	fx.confirm(t, japanese, finish.Nonfoil)

	if err := CompleteAdd(context.Background(), fx.deps, japanese, finish.Nonfoil); err == nil {
		t.Error("fetching a different printing was reported as success")
	}

	if _, err := fx.store.CardDetail("english-twin"); err == nil {
		t.Error("a document for a different printing was stored as a new card")
	}
	rows, err := fx.store.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].ScryfallID != "jp-id" {
		t.Errorf("collection = %+v, want only the printing that was added", rows)
	}
}

func TestAddCardByURLLeavesTheCardWhole(t *testing.T) {
	doc := solDocumentCard()
	fx := newAddFixture(t, &doc, nil)
	writeCachedArchive(t, fx.cacheDir, time.Now().Format("2006-01-02"),
		`{"data": {"uuid-sol": {"paper": {"tcgplayer": {"currency": "USD",
			"retail": {"normal": {"2026-08-20": 2.00, "2026-08-21": 2.10,
			"2026-08-22": 2.25, "2026-08-23": 2.20, "2026-08-24": 2.40}}}}}}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		zw := gzip.NewWriter(w)
		io.WriteString(zw, `{"data": {"cards": [{"uuid": "uuid-sol",
			"identifiers": {"scryfallId": "sol-id-1"}}]}}`)
		zw.Close()
	}))
	defer srv.Close()
	fx.deps.PriceBaseURL = srv.URL

	res, err := AddCardByURL(context.Background(), fx.deps, nil, AddCardByURLOptions{
		URL: "https://scryfall.com/card/c21/125/sol-ring", Qty: 1})
	if err != nil {
		t.Fatalf("AddCardByURL: %v", err)
	}
	if res.Card.ID != "sol-id-1" {
		t.Fatalf("added %s (%s/%s), want the card the injected individual route returned; "+
			"the fetch went past the resolver to the live API",
			res.Card.Name, res.Card.Set, res.Card.CollectorNumber)
	}

	if fx.bulk != 0 {
		t.Errorf("the bulk collection route was called %d times, want never", fx.bulk)
	}
	if len(fx.asked) != 1 || fx.asked[0] != "c21/125/" {
		t.Errorf("card route asked for %v, want exactly [c21/125/]", fx.asked)
	}

	d, err := fx.store.CardDetail("sol-id-1")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if !d.Enriched {
		t.Error("the card was stored with no document")
	}

	series, err := fx.store.PriceSeries("sol-id-1", finish.Nonfoil)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) < 6 {
		t.Errorf("sparkline has %d points, want the archive's five days plus today's",
			len(series))
	}
}
