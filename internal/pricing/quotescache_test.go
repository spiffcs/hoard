package pricing

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

func quoteFixture() map[string][]mtgjson.Quote {
	return map[string][]mtgjson.Quote{
		"sol-id": {
			{Provider: "tcgplayer", Kind: mtgjson.Retail, Finish: finish.Nonfoil, Price: 2.5},
			{Provider: "cardkingdom", Kind: mtgjson.Buylist, Finish: finish.Nonfoil, Price: 1.1},
		},
	}
}

func TestQuotesDayCacheRoundTrips(t *testing.T) {
	f := New(nil, t.TempDir())
	refs := []Ref{{ScryfallID: "sol-id"}, {ScryfallID: "remora-id"}}
	f.saveQuotes(refs, quoteFixture())

	got, ok := f.cachedQuotes(refs)
	if !ok {
		t.Fatal("cache miss immediately after save")
	}
	if len(got["sol-id"]) != 2 || got["sol-id"][0].Provider != "tcgplayer" {
		t.Errorf("quotes = %+v, want the two saved for sol-id", got["sol-id"])
	}
	if _, present := got["remora-id"]; present {
		t.Error("remora-id gained quotes it never had")
	}
}

func TestQuotesDayCacheMissesOnUnseenCard(t *testing.T) {
	f := New(nil, t.TempDir())
	f.saveQuotes([]Ref{{ScryfallID: "sol-id"}}, quoteFixture())

	if _, ok := f.cachedQuotes([]Ref{{ScryfallID: "sol-id"}, {ScryfallID: "new-id"}}); ok {
		t.Error("cache served a card the parse never saw")
	}
}

func TestQuotesDayCacheDisabledWithoutDir(t *testing.T) {
	f := New(nil, "")
	f.saveQuotes([]Ref{{ScryfallID: "sol-id"}}, quoteFixture())
	if _, ok := f.cachedQuotes([]Ref{{ScryfallID: "sol-id"}}); ok {
		t.Error("an empty cache dir produced a cache hit")
	}
}

func TestQuotesServedFromDayCacheWithoutStoreOrNetwork(t *testing.T) {
	f := New(nil, t.TempDir())
	refs := []Ref{{ScryfallID: "sol-id"}}
	f.saveQuotes(refs, quoteFixture())

	got, err := f.Quotes(context.Background(), refs)
	if err != nil {
		t.Fatalf("Quotes: %v", err)
	}
	if len(got["sol-id"]) != 2 {
		t.Errorf("quotes = %+v, want the cached pair", got["sol-id"])
	}
}
