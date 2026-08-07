package hoardjson

// Every test compares exact bytes, not parsed equivalence: the emitted JSON is
// a compatibility surface, and a reordered field or reformatted number is a
// break these tests exist to catch.

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

func write(t *testing.T, doc Document) string {
	t.Helper()
	var sb strings.Builder
	if err := Write(&sb, doc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return sb.String()
}

func f(v float64) *float64 { return &v }

func TestSummaryDocument(t *testing.T) {
	got := write(t, FromSummary(
		store.CollectionTotals{DistinctCards: 2, TotalCopies: 3, Value: 14.5},
		[]store.DeckSummary{
			{Container: store.Container{Name: "Fish"}, DistinctCards: 1, TotalCopies: 1, Value: 0},
			{Container: store.Container{Name: "Bears"}, DistinctCards: 1, TotalCopies: 4, Value: 9},
		}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "summary",
  "summary": {
    "binder": {
      "distinctCards": 2,
      "totalCopies": 3,
      "valueUsd": 14.5
    },
    "decks": [
      {
        "name": "Bears",
        "distinctCards": 1,
        "totalCopies": 4,
        "valueUsd": 9
      },
      {
        "name": "Fish",
        "distinctCards": 1,
        "totalCopies": 1,
        "valueUsd": 0
      }
    ],
    "total": {
      "totalCopies": 8,
      "valueUsd": 23.5
    }
  }
}
`
	if got != want {
		t.Errorf("summary document:\n%s\nwant:\n%s", got, want)
	}
}

func TestHoldingsDocumentSortsAndOmitsAbsentValues(t *testing.T) {
	// Rows arrive out of canonical order; the document must sort them exactly
	// as the canonical CSV would. The unpriced row must omit priceUsd — not
	// carry null, not carry zero — and an unmapped card omits mtgjsonUuid.
	got := write(t, FromExportRows([]export.Row{
		{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78",
			Finish: "nonfoil", ScryfallID: "rem", Container: "Fish", Kind: "deck", Board: "main"},
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: "nonfoil", ScryfallID: "sol", MTGJSONUUID: "uu-sol", Container: "Binder",
			Kind: "binder", Board: "main", PriceUSD: f(2)},
	}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "holdings",
  "holdings": {
    "rows": [
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "mtgjsonUuid": "uu-sol",
          "setCode": "c21",
          "number": "125",
          "finish": "nonfoil"
        },
        "count": 2,
        "container": "Binder",
        "containerKind": "binder",
        "board": "main",
        "priceUsd": 2
      },
      {
        "card": {
          "name": "Mystic Remora",
          "scryfallId": "rem",
          "setCode": "ice",
          "number": "78",
          "finish": "nonfoil"
        },
        "count": 1,
        "container": "Fish",
        "containerKind": "deck",
        "board": "main"
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("holdings document:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnpricedDocument(t *testing.T) {
	got := write(t, FromUnpriced([]store.UnpricedRow{{
		ScryfallID: "rem", MTGJSONUUID: "uu-rem", Name: "Mystic Remora",
		SetCode: "ice", CollectorNumber: "78", Finish: "nonfoil", Copies: 3,
		Containers: []string{"Binder", "Fish"}, HeldIn: "Binder,Fish",
	}}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "unpriced",
  "unpriced": {
    "rows": [
      {
        "card": {
          "name": "Mystic Remora",
          "scryfallId": "rem",
          "mtgjsonUuid": "uu-rem",
          "setCode": "ice",
          "number": "78",
          "finish": "nonfoil"
        },
        "copies": 3,
        "containers": [
          "Binder",
          "Fish"
        ]
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("unpriced document:\n%s\nwant:\n%s", got, want)
	}
}

func TestMoversDocumentOrdersByAbsoluteImpact(t *testing.T) {
	// The $0.50 sinker on forty copies outweighs the $2 riser on one copy, so
	// it must lead despite being a fall — the interleaved-by-magnitude order
	// MoversByImpact promises.
	got := write(t, FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
		[]store.PriceChange{
			{ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma", CollectorNumber: "236",
				Finish: "nonfoil", Copies: 1, Old: 30, New: 32, Source: "scryfall"},
			{ScryfallID: "b", Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
				Finish: "nonfoil", Copies: 40, Old: 2, New: 1.5, Source: "cardkingdom"},
		}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "movers",
  "movers": {
    "since": "2026-06-30T00:00:00Z",
    "recordedSince": "2026-07-01T09:00:00Z",
    "changes": [
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "b",
          "setCode": "c21",
          "number": "125",
          "finish": "nonfoil"
        },
        "copies": 40,
        "oldUsd": 2,
        "newUsd": 1.5,
        "impactUsd": -20,
        "source": "cardkingdom"
      },
      {
        "card": {
          "name": "Ancient Tomb",
          "scryfallId": "a",
          "setCode": "uma",
          "number": "236",
          "finish": "nonfoil"
        },
        "copies": 1,
        "oldUsd": 30,
        "newUsd": 32,
        "impactUsd": 2,
        "source": "scryfall"
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("movers document:\n%s\nwant:\n%s", got, want)
	}
}

func TestMoversDocumentWithNoHistory(t *testing.T) {
	got := write(t, FromMovers("2026-06-30T00:00:00Z", "", nil))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "movers",
  "movers": {
    "since": "2026-06-30T00:00:00Z",
    "changes": []
  }
}
`
	if got != want {
		t.Errorf("empty movers document:\n%s\nwant:\n%s", got, want)
	}
}

func TestArbitrageDocumentTagsEveryQuestion(t *testing.T) {
	// tomb answers both the profit and the below-market question, so it
	// appears twice with different kinds; ring has no buylist, so its
	// sell-side fields are absent, not zero.
	tomb := market.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "a", MTGJSONUUID: "uu-a", Name: "Ancient Tomb",
			SetCode: "uma", CollectorNumber: "236", Finish: "nonfoil", Copies: 1, Value: 60},
		Market: 4, BuyAt: 2, BuyFrom: "cardmarket",
		SellAt: 5, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true,
	}
	ring := market.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "b", Name: "Sol Ring",
			SetCode: "c21", CollectorNumber: "125", Finish: "foil", Copies: 2, Value: 25},
		Market: 20, BuyAt: 10, BuyFrom: "cardkingdom",
		HasMarket: true, HasRetail: true,
	}
	got := write(t, FromMarket(market.Result{
		Opportunities: []market.Opportunity{tomb, ring}, Compared: 2,
	}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "market",
  "market": {
    "comparedPrintings": 2,
    "opportunities": [
      {
        "card": {
          "name": "Ancient Tomb",
          "scryfallId": "a",
          "mtgjsonUuid": "uu-a",
          "setCode": "uma",
          "number": "236",
          "finish": "nonfoil"
        },
        "copies": 1,
        "valueUsd": 60,
        "kind": "arbitrage",
        "buyUsd": 2,
        "buyFrom": "cardmarket",
        "marketUsd": 4,
        "belowMarket": 0.5,
        "sellUsd": 5,
        "sellTo": "cardkingdom",
        "profitUsd": 1,
        "liquidity": 1.25
      },
      {
        "card": {
          "name": "Ancient Tomb",
          "scryfallId": "a",
          "mtgjsonUuid": "uu-a",
          "setCode": "uma",
          "number": "236",
          "finish": "nonfoil"
        },
        "copies": 1,
        "valueUsd": 60,
        "kind": "below-market",
        "buyUsd": 2,
        "buyFrom": "cardmarket",
        "marketUsd": 4,
        "belowMarket": 0.5,
        "sellUsd": 5,
        "sellTo": "cardkingdom",
        "profitUsd": 1,
        "liquidity": 1.25
      },
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "b",
          "setCode": "c21",
          "number": "125",
          "finish": "foil"
        },
        "copies": 2,
        "valueUsd": 25,
        "kind": "below-market",
        "buyUsd": 10,
        "buyFrom": "cardkingdom",
        "marketUsd": 20,
        "belowMarket": 0.5
      }
    ],
    "comps": []
  }
}
`
	if got != want {
		t.Errorf("arbitrage document:\n%s\nwant:\n%s", got, want)
	}
}

func TestReportDocument(t *testing.T) {
	got := write(t, FromValuation(report.ValuationData{
		AsOf:   "2026-07-30T09:00:00Z",
		Binder: store.CollectionTotals{DistinctCards: 2, TotalCopies: 3, Value: 16.5},
		Binders: []store.DeckSummary{
			{Container: store.Container{Name: "Binder"}, DistinctCards: 1, TotalCopies: 2, Value: 4},
			{Container: store.Container{Name: "Trade"}, DistinctCards: 1, TotalCopies: 1, Value: 12.5},
		},
		Decks: []store.DeckSummary{
			{Container: store.Container{Name: "Fish"}, DistinctCards: 1, TotalCopies: 1, Value: 0},
		},
		Top: []store.OwnedFinish{
			{ScryfallID: "sol", MTGJSONUUID: "uu-sol", Name: "Sol Ring", SetCode: "c21",
				CollectorNumber: "125", Finish: "foil", Copies: 1, Value: 12.5},
			{ScryfallID: "rem", Name: "Mystic Remora", SetCode: "ice",
				CollectorNumber: "78", Finish: "nonfoil", Copies: 1, Value: 0},
		},
		Sources:  []store.SourceCount{{Source: "scryfall", Printings: 2, Copies: 3}},
		Unpriced: store.SourceCount{Printings: 1, Copies: 1},
	}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "report",
  "report": {
    "asOf": "2026-07-30T09:00:00Z",
    "total": {
      "totalCopies": 4,
      "valueUsd": 16.5
    },
    "binder": {
      "distinctCards": 2,
      "totalCopies": 3,
      "valueUsd": 16.5
    },
    "decks": {
      "count": 1,
      "totalCopies": 1,
      "valueUsd": 0
    },
    "binders": [
      {
        "name": "Binder",
        "distinctCards": 1,
        "totalCopies": 2,
        "valueUsd": 4
      },
      {
        "name": "Trade",
        "distinctCards": 1,
        "totalCopies": 1,
        "valueUsd": 12.5
      }
    ],
    "topHoldings": [
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "mtgjsonUuid": "uu-sol",
          "setCode": "c21",
          "number": "125",
          "finish": "foil"
        },
        "copies": 1,
        "priceUsd": 12.5,
        "valueUsd": 12.5
      },
      {
        "card": {
          "name": "Mystic Remora",
          "scryfallId": "rem",
          "setCode": "ice",
          "number": "78",
          "finish": "nonfoil"
        },
        "copies": 1,
        "valueUsd": 0
      }
    ],
    "sources": [
      {
        "source": "scryfall",
        "printings": 2,
        "copies": 3
      }
    ],
    "unpriced": {
      "printings": 1,
      "copies": 1
    }
  }
}
`
	if got != want {
		t.Errorf("report document:\n%s\nwant:\n%s", got, want)
	}
}

func TestWatchDocument(t *testing.T) {
	got := write(t, FromWatchCheck(3, []store.WatchStatus{{
		Watch: store.Watch{ScryfallID: "sol", Finish: "foil", Op: "over", Threshold: 10},
		Name:  "Sol Ring", SetCode: "c21", CollectorNumber: "125",
		MTGJSONUUID: "uu-sol", PriceUSD: f(12.5),
	}}))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "watch",
  "watch": {
    "checked": 3,
    "fired": [
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "mtgjsonUuid": "uu-sol",
          "setCode": "c21",
          "number": "125",
          "finish": "foil"
        },
        "op": "over",
        "thresholdUsd": 10,
        "priceUsd": 12.5
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("watch document:\n%s\nwant:\n%s", got, want)
	}
}

func TestWatchDocumentWithNothingFired(t *testing.T) {
	got := write(t, FromWatchCheck(2, nil))
	want := `{
  "schemaVersion": "1.1.4",
  "kind": "watch",
  "watch": {
    "checked": 2,
    "fired": []
  }
}
`
	if got != want {
		t.Errorf("quiet watch document:\n%s\nwant:\n%s", got, want)
	}
}

// The market document carries the comp sheets: vendor fields omitted when
// absent, spread an unrounded fraction, rows in the value order Collect
// established.
func TestMarketDocumentCarriesComps(t *testing.T) {
	full := market.Comp{
		Card: store.OwnedFinish{ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma",
			CollectorNumber: "236", Finish: "foil", Copies: 1, Value: 60},
		Market: 60, HasMarket: true, CK: 65, HasCK: true,
		Low: 60, LowFrom: "tcgplayer",
		Buylist: 42, BuylistTo: "cardkingdom", HasBuylist: true,
	}
	bare := market.Comp{
		Card: store.OwnedFinish{ScryfallID: "b", Name: "Sol Ring", SetCode: "c21",
			CollectorNumber: "125", Finish: "nonfoil", Copies: 2, Value: 4},
		Low: 1.99, LowFrom: "manapool", Manapool: 1.99, HasManapool: true,
	}
	doc := FromMarket(market.Result{Comps: []market.Comp{full, bare}, Compared: 2})

	comps := doc.Market.Comps
	if len(comps) != 2 {
		t.Fatalf("comps = %d rows", len(comps))
	}
	c := comps[0]
	if c.MarketUsd == nil || *c.MarketUsd != 60 || c.CardKingdomUsd == nil || c.ManapoolUsd != nil {
		t.Errorf("vendor fields = %+v, want present-only-when-quoted", c)
	}
	if c.Spread == nil || *c.Spread < 0.2999 || *c.Spread > 0.3001 {
		t.Errorf("spread = %v, want the 30%% fraction", c.Spread)
	}
	b := comps[1]
	if b.Spread != nil || b.BuylistUsd != nil {
		t.Errorf("no buylist must mean no spread: %+v", b)
	}
	if b.LowUsd != 1.99 || b.LowFrom != "manapool" {
		t.Errorf("low = %v from %q", b.LowUsd, b.LowFrom)
	}
}

// A condition rides the holdings document, and an unassessed one is absent
// rather than spelled "unknown": the document's rule is that absent means
// unknown, and almost every holding is unassessed.
func TestHoldingsDocumentCarriesCondition(t *testing.T) {
	out := write(t, FromExportRows([]export.Row{
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: "nonfoil", Condition: "lp", ScryfallID: "sol",
			Container: "Binder", Kind: "binder", Board: "main"},
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: "nonfoil", Condition: "unknown", ScryfallID: "sol",
			Container: "Binder", Kind: "binder", Board: "main"},
	}))
	if !strings.Contains(out, `"condition": "lp"`) {
		t.Errorf("assessed row lost its condition:\n%s", out)
	}
	if strings.Contains(out, `"unknown"`) {
		t.Errorf("unassessed row emitted the word rather than omitting the field:\n%s", out)
	}
}
