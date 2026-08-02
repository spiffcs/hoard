package hoardjson

// Every test compares exact bytes, not parsed equivalence: the emitted JSON is
// a compatibility surface, and a reordered field or reformatted number is a
// break these tests exist to catch.

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/export"
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
	tomb := arbitrage.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "a", MTGJSONUUID: "uu-a", Name: "Ancient Tomb",
			SetCode: "uma", CollectorNumber: "236", Finish: "nonfoil", Copies: 1, Value: 60},
		Market: 4, BuyAt: 2, BuyFrom: "cardmarket",
		SellAt: 5, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true,
	}
	ring := arbitrage.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "b", Name: "Sol Ring",
			SetCode: "c21", CollectorNumber: "125", Finish: "foil", Copies: 2, Value: 25},
		Market: 20, BuyAt: 10, BuyFrom: "cardkingdom",
		HasMarket: true, HasRetail: true,
	}
	got := write(t, FromArbitrage(arbitrage.Result{
		Opportunities: []arbitrage.Opportunity{tomb, ring}, Compared: 2,
	}))
	want := `{
  "schemaVersion": "1.1.0",
  "kind": "arbitrage",
  "arbitrage": {
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
    ]
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
  "schemaVersion": "1.1.0",
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
