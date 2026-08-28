package hoardjson

// Every test compares exact bytes, not parsed equivalence: the emitted JSON is
// a compatibility surface, and a reordered field or reformatted number is a
// break these tests exist to catch.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/finish"
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
			{Container: store.Container{Name: "Fish"}, DistinctCards: 1, TotalCopies: 1, Value: 0,
				Counted: true},
			{Container: store.Container{Name: "Bears"}, DistinctCards: 1, TotalCopies: 4, Value: 9,
				Counted: true},
		}))
	want := `{
  "schemaVersion": "1.2.1",
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
			Finish: finish.Nonfoil, ScryfallID: "rem", ContainerID: 3, Container: "Fish",
			Kind: "deck", Board: "main"},
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: finish.Nonfoil, ScryfallID: "sol", MTGJSONUUID: "uu-sol",
			ContainerID: 1, Container: "Binder",
			Kind: "binder", Board: "main", PriceUSD: f(2)},
	}))
	want := `{
  "schemaVersion": "1.2.1",
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
        "containerId": 1,
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
        "containerId": 3,
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
		SetCode: "ice", CollectorNumber: "78", Finish: finish.Nonfoil, Copies: 3,
		Containers: []string{"Binder", "Fish"}, HeldIn: "Binder,Fish",
	}}))
	want := `{
  "schemaVersion": "1.2.1",
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
	// Ancient Tomb's record begins inside the window and Sol Ring's reaches its
	// start, so the two carry different oldAsOf — the whole reason a consumer
	// cannot read oldUsd as "the price at since".
	got := write(t, FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
		[]store.PriceChange{
			{ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma", CollectorNumber: "236",
				Finish: finish.Nonfoil, Copies: 1, Old: 30, New: 32, Source: "scryfall",
				OldAsOf: "2026-07-05T00:00:00Z"},
			{ScryfallID: "b", Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
				Finish: finish.Nonfoil, Copies: 40, Old: 2, New: 1.5, Source: "cardkingdom",
				OldAsOf: "2026-06-30T00:00:00Z"},
		}))
	want := `{
  "schemaVersion": "1.2.1",
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
        "oldAsOf": "2026-06-30T00:00:00Z",
        "impactUsd": -20,
        "pctChange": -0.25,
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
        "oldAsOf": "2026-07-05T00:00:00Z",
        "impactUsd": 2,
        "pctChange": 0.06666666666666667,
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
  "schemaVersion": "1.2.1",
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
			SetCode: "uma", CollectorNumber: "236", Finish: finish.Nonfoil, Copies: 1, Value: 60},
		Market: 4, BuyAt: 2, BuyFrom: "cardmarket",
		SellAt: 5, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true,
	}
	ring := market.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "b", Name: "Sol Ring",
			SetCode: "c21", CollectorNumber: "125", Finish: finish.Foil, Copies: 2, Value: 25},
		Market: 20, BuyAt: 10, BuyFrom: "cardkingdom",
		HasMarket: true, HasRetail: true,
	}
	got := write(t, FromMarket(market.Result{
		Opportunities: []market.Opportunity{tomb, ring}, Compared: 2,
	}))
	want := `{
  "schemaVersion": "1.2.1",
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
        "profitUsd": 3,
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
        "profitUsd": 3,
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
			{Container: store.Container{Name: "Fish"}, DistinctCards: 1, TotalCopies: 1, Value: 0,
				Counted: true},
		},
		Top: []store.OwnedFinish{
			{ScryfallID: "sol", MTGJSONUUID: "uu-sol", Name: "Sol Ring", SetCode: "c21",
				CollectorNumber: "125", Finish: finish.Foil, Copies: 1, Value: 12.5},
			{ScryfallID: "rem", Name: "Mystic Remora", SetCode: "ice",
				CollectorNumber: "78", Finish: finish.Nonfoil, Copies: 1, Value: 0},
		},
		Sources:  []store.SourceCount{{Source: "scryfall", Printings: 2, Copies: 3}},
		Unpriced: store.SourceCount{Printings: 1, Copies: 1},
	}))
	want := `{
  "schemaVersion": "1.2.1",
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
		Watch: store.Watch{ScryfallID: "sol", Finish: finish.Foil, Op: "over", Threshold: 10},
		Name:  "Sol Ring", SetCode: "c21", CollectorNumber: "125",
		MTGJSONUUID: "uu-sol", PriceUSD: f(12.5),
	}}))
	want := `{
  "schemaVersion": "1.2.1",
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
  "schemaVersion": "1.2.1",
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
			CollectorNumber: "236", Finish: finish.Foil, Copies: 1, Value: 60},
		Market: 60, HasMarket: true, CK: 65, HasCK: true,
		Low: 60, LowFrom: "tcgplayer",
		Buylist: 42, BuylistTo: "cardkingdom", HasBuylist: true,
	}
	bare := market.Comp{
		Card: store.OwnedFinish{ScryfallID: "b", Name: "Sol Ring", SetCode: "c21",
			CollectorNumber: "125", Finish: finish.Nonfoil, Copies: 2, Value: 4},
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
			Finish: finish.Nonfoil, Condition: "lp", ScryfallID: "sol",
			Container: "Binder", Kind: "binder", Board: "main"},
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: finish.Nonfoil, Condition: "unknown", ScryfallID: "sol",
			Container: "Binder", Kind: "binder", Board: "main"},
	}))
	if !strings.Contains(out, `"condition": "lp"`) {
		t.Errorf("assessed row lost its condition:\n%s", out)
	}
	if strings.Contains(out, `"unknown"`) {
		t.Errorf("unassessed row emitted the word rather than omitting the field:\n%s", out)
	}
}

// colorIdentityRows are the three states the field has to keep apart: a
// colorless card, a card with colors, and a printing whose document hoard has
// never fetched. store.Card's semantics — nil is unknown, empty is colorless —
// arrive here through export.Row unchanged.
func colorIdentityRows() []export.Row {
	return []export.Row{
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: finish.Nonfoil, ScryfallID: "sol", ColorIdentity: []string{},
			Container: "Binder", Kind: "binder", Board: "main"},
		{Count: 1, Name: "Swamp", Set: "c21", CollectorNumber: "300",
			Finish: finish.Nonfoil, ScryfallID: "swp", ColorIdentity: []string{"B"},
			Container: "Binder", Kind: "binder", Board: "main"},
		{Count: 1, Name: "Unfetched Card", Set: "xxx", CollectorNumber: "1",
			Finish: finish.Nonfoil, ScryfallID: "unf",
			Container: "Binder", Kind: "binder", Board: "main"},
	}
}

// identityOf reports what one named row's card says about its identity: the
// decoded letters, and whether the key was there at all. Absent and `[]` are
// different answers, so a test cannot ask this question with len() alone.
func identityOf(t *testing.T, doc string, name string) (letters []string, present bool) {
	t.Helper()
	var parsed struct {
		Holdings struct {
			Rows []struct {
				Card map[string]json.RawMessage `json:"card"`
			} `json:"rows"`
		} `json:"holdings"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("parsing the emitted document: %v", err)
	}
	for _, row := range parsed.Holdings.Rows {
		var got string
		if err := json.Unmarshal(row.Card["name"], &got); err != nil {
			t.Fatalf("parsing a card name: %v", err)
		}
		if got != name {
			continue
		}
		raw, ok := row.Card["colorIdentity"]
		if !ok {
			return nil, false
		}
		if err := json.Unmarshal(raw, &letters); err != nil {
			t.Fatalf("parsing %s's colorIdentity: %v", name, err)
		}
		return letters, true
	}
	t.Fatalf("no row for %q in:\n%s", name, doc)
	return nil, false
}

// The schema hoard ships gives absence and `[]` different meanings —
// "identity not known to hoard" and "colorless" — so the emission has to keep
// them apart. omitempty cannot: it drops an empty slice, which reported every
// colorless card as one hoard knows nothing about (338 of 1,768 rows on the
// owner's collection, all of them stored as `[]`). Hence the pointer.
func TestHoldingsDocumentDistinguishesColorlessFromUnknown(t *testing.T) {
	out := write(t, FromExportRows(colorIdentityRows()))

	if letters, present := identityOf(t, out, "Sol Ring"); !present || len(letters) != 0 {
		t.Errorf("a colorless card must emit an empty colorIdentity, got %v (present %v):\n%s",
			letters, present, out)
	}
	if !strings.Contains(out, `"colorIdentity": []`) {
		t.Errorf("no colorless card emitted the empty array the schema defines:\n%s", out)
	}
	if letters, present := identityOf(t, out, "Swamp"); !present || !reflect.DeepEqual(letters, []string{"B"}) {
		t.Errorf("colored card's identity = %v (present %v), want [B]", letters, present)
	}
	if letters, present := identityOf(t, out, "Unfetched Card"); present {
		t.Errorf("an unfetched printing must omit colorIdentity entirely, got %v:\n%s", letters, out)
	}
}

// The distinction has to survive the round trip too. `hoard merge` writes a
// document, reads it back and plans from what it read, and identifies the
// merge by hashing those bytes — so re-encoding what was read must reproduce
// them, colorless rows included.
func TestColorIdentityRoundTripsAndReEncodesIdentically(t *testing.T) {
	first := write(t, FromExportRows(colorIdentityRows()))

	doc, err := Read(strings.NewReader(first))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var again bytes.Buffer
	if err := Write(&again, doc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if again.String() != first {
		t.Fatalf("re-encoding a document that was read back differs:\n got %s\nwant %s",
			again.String(), first)
	}

	// Asserted on the re-encoded bytes, not the first ones: a field that
	// decodes into a state it cannot re-emit passes the emission test and
	// still loses the fact on the way through.
	if letters, present := identityOf(t, again.String(), "Sol Ring"); !present || len(letters) != 0 {
		t.Errorf("colorless became %v (present %v) through the round trip", letters, present)
	}
	if _, present := identityOf(t, again.String(), "Unfetched Card"); present {
		t.Error("an unfetched printing gained a colorIdentity through the round trip")
	}
}

// A hoard document survives the round trip that `hoard merge` puts it
// through — write, read back, plan from what was read. The card document in
// Raw is the field most likely to be mangled by an encoder, so it is the one
// asserted byte-for-byte.
func TestHoardDocumentRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"rarity":"mythic","card_faces":[{"type_line":"Legendary Creature — Eldrazi"}]}`)
	doc := Document{
		SchemaVersion: SchemaVersion,
		Kind:          KindHoard,
		Hoard: &Hoard{
			DatabaseVersion: 27,
			Printings: []Printing{{
				ScryfallID: "u-id", Name: "Ulamog", SetCode: "uma", Number: "7",
				ScryfallURL: "https://scryfall.com/card/uma/7",
				UpdatedAt:   "2026-08-09T00:00:00Z",
				MTGJSONUUID: "abc", PriceUsd: f(10.5), Raw: raw,
			}},
			Containers: []Container{
				{Name: "Collection", Kind: "binder", Source: "manual"},
				{Name: "Superfriends", Kind: "deck", Source: "archidekt",
					SourceID: "111", Format: "commander"},
			},
			Holdings: Holdings{Rows: []Holding{{
				Card:  Card{Name: "Ulamog", ScryfallID: "u-id", SetCode: "uma", Number: "7", Finish: "foil"},
				Count: 2, Container: "Collection", ContainerKind: "binder", Board: "main",
			}}},
			Watches: []Watch{{
				Card:      Card{Name: "Ulamog", ScryfallID: "u-id", SetCode: "uma", Number: "7", Finish: "foil"},
				Op:        "under",
				Threshold: 5, Display: "Ulamog",
			}},
		},
	}

	var buf bytes.Buffer
	if err := Write(&buf, doc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	first := buf.String()

	got, err := ReadHoard(strings.NewReader(first))
	if err != nil {
		t.Fatalf("ReadHoard: %v", err)
	}

	// Raw is compared as JSON rather than as bytes. Write indents the whole
	// document, embedded card documents included, so what comes back is the
	// same JSON pretty-printed — which is why action.planMerge compacts it
	// again before storing.
	if !json.Valid(got.Printings[0].Raw) {
		t.Fatalf("the card document did not survive: %s", got.Printings[0].Raw)
	}
	var gotRaw, wantRaw any
	if err := json.Unmarshal(got.Printings[0].Raw, &gotRaw); err != nil {
		t.Fatalf("unmarshalling the round-tripped document: %v", err)
	}
	if err := json.Unmarshal(raw, &wantRaw); err != nil {
		t.Fatalf("unmarshalling the original document: %v", err)
	}
	if !reflect.DeepEqual(gotRaw, wantRaw) {
		t.Errorf("card document changed:\n got %s\nwant %s", got.Printings[0].Raw, raw)
	}

	got.Printings[0].Raw, doc.Hoard.Printings[0].Raw = nil, nil
	if !reflect.DeepEqual(*got, *doc.Hoard) {
		t.Errorf("round trip changed the payload:\n got %+v\nwant %+v", *got, *doc.Hoard)
	}
	got.Printings[0].Raw = raw

	// Writing what was read must reproduce the same bytes. `hoard merge`
	// identifies a merge by hashing them, so an unstable encoding would
	// silently disable the guard against merging twice.
	var again bytes.Buffer
	if err := Write(&again, Document{SchemaVersion: SchemaVersion, Kind: KindHoard, Hoard: got}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if again.String() != first {
		t.Errorf("re-encoding differs:\n got %s\nwant %s", again.String(), first)
	}
}

// A document from a future MODEL is refused rather than half-understood; a
// higher ADDITION is fine, since the unknown fields are the ignorable ones.
func TestReadRejectsNewerModel(t *testing.T) {
	if _, err := Read(strings.NewReader(`{"schemaVersion":"2.0.0","kind":"hoard"}`)); err == nil {
		t.Error("a MODEL 2 document was accepted by a MODEL 1 build")
	}
	if _, err := Read(strings.NewReader(`{"schemaVersion":"1.9.9","kind":"summary"}`)); err != nil {
		t.Errorf("a later ADDITION was refused: %v", err)
	}
	if _, err := Read(strings.NewReader(`{"kind":"hoard"}`)); err == nil {
		t.Error("a document with no schemaVersion was accepted")
	}
}

// ReadHoard insists on the kind it names.
func TestReadHoardRejectsOtherKinds(t *testing.T) {
	_, err := ReadHoard(strings.NewReader(`{"schemaVersion":"1.0.0","kind":"summary"}`))
	if err == nil {
		t.Fatal("a summary document was accepted as a hoard")
	}
	if !strings.Contains(err.Error(), "not a") {
		t.Errorf("error was %q", err)
	}
}

func i64(v int64) *int64 { return &v }

func str(v string) *string { return &v }

// detailRows are the two states the detail object has to keep apart within one
// printing's worth of data: a creature, which has a power and a toughness, and
// an artifact, which has neither. Coverage varies field by field — the store
// reads these out of the printing's Scryfall document, and a card simply does
// not have every kind of value — so "absent" here must mean "no such value",
// never "not looked up".
func detailRows() []export.Row {
	return []export.Row{
		{Count: 1, Name: "Llanowar Elves", Set: "dom", CollectorNumber: "168",
			Finish: finish.Nonfoil, ScryfallID: "elf", ContainerID: 1, Container: "Binder",
			Kind: "binder", Board: "main",
			Detail: &store.CardDetail{
				Card:   store.Card{ManaCost: str("{G}")},
				Rarity: "common", TypeLine: "Creature — Elf Druid", CMC: f(1),
				OracleText: "{T}: Add {G}.",
				Power:      "1", Toughness: "1",
				SetName: "Dominaria", ReleasedAt: "2018-04-27",
				Artist: "Chris Rahn", Layout: "normal",
				TCGplayerID: i64(161475),
			}},
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: finish.Foil, ScryfallID: "sol", ContainerID: 1, Container: "Binder",
			Kind: "binder", Board: "main", PriceUSD: f(2),
			Detail: &store.CardDetail{
				Card:   store.Card{ManaCost: str("{1}")},
				Rarity: "uncommon", TypeLine: "Artifact", CMC: f(1),
				OracleText: "{T}: Add {C}{C}.",
				SetName:    "Commander 2021", ReleasedAt: "2021-04-23",
				Artist: "Mike Bierek", Layout: "normal",
				PromoTypes:  []string{"surgefoil"},
				TCGplayerID: i64(235854),
			}},
	}
}

// The holdings document is the one place these fields live, and this is their
// exact shape: `detail` beside `card`, every field omitted where the card has
// no such value. Exact bytes, because the emission is a compatibility surface.
func TestHoldingsDocumentCarriesDetail(t *testing.T) {
	got := write(t, FromExportRows(detailRows()))
	want := `{
  "schemaVersion": "1.2.1",
  "kind": "holdings",
  "holdings": {
    "rows": [
      {
        "card": {
          "name": "Llanowar Elves",
          "scryfallId": "elf",
          "setCode": "dom",
          "number": "168",
          "finish": "nonfoil"
        },
        "detail": {
          "rarity": "common",
          "typeLine": "Creature — Elf Druid",
          "cmc": 1,
          "manaCost": "{G}",
          "oracleText": "{T}: Add {G}.",
          "power": "1",
          "toughness": "1",
          "setName": "Dominaria",
          "releasedAt": "2018-04-27",
          "artist": "Chris Rahn",
          "layout": "normal",
          "tcgplayerId": 161475
        },
        "count": 1,
        "containerId": 1,
        "container": "Binder",
        "containerKind": "binder",
        "board": "main"
      },
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "setCode": "c21",
          "number": "125",
          "finish": "foil"
        },
        "detail": {
          "rarity": "uncommon",
          "typeLine": "Artifact",
          "cmc": 1,
          "manaCost": "{1}",
          "oracleText": "{T}: Add {C}{C}.",
          "setName": "Commander 2021",
          "releasedAt": "2021-04-23",
          "artist": "Mike Bierek",
          "layout": "normal",
          "promoTypes": [
            "surgefoil"
          ],
          "tcgplayerId": 235854
        },
        "count": 2,
        "containerId": 1,
        "container": "Binder",
        "containerKind": "binder",
        "board": "main",
        "priceUsd": 2
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("holdings document with detail:\n%s\nwant:\n%s", got, want)
	}
}

// A creature carries power and toughness; an artifact must not carry them as
// empty strings. This is the same defect class as a colorless card reporting
// an unknown identity: a field the encoder writes where there is nothing to
// say tells a consumer the card has a value of "".
func TestDetailOmitsFieldsTheCardHasNoValueFor(t *testing.T) {
	out := write(t, FromExportRows(detailRows()))
	if strings.Count(out, `"power"`) != 1 || strings.Count(out, `"toughness"`) != 1 {
		t.Errorf("power/toughness must appear once — on the creature only:\n%s", out)
	}
	if strings.Contains(out, `""`) {
		t.Errorf("a field with no value was emitted as an empty string:\n%s", out)
	}
	for _, absent := range []string{"loyalty", "flavorText", "printedName"} {
		if strings.Contains(out, `"`+absent+`"`) {
			t.Errorf("%s has no value on either card and must be omitted:\n%s", absent, out)
		}
	}
}

// A printing hoard has stored no Scryfall document for has no detail at all —
// not an object of empty strings, and not an empty object either. The store
// leaves it out of the map, action turns that into a nil row field, and that
// has to survive to the document: the MISSING OBJECT is the only way this
// format says "nobody has looked", since an empty field inside it means the
// much weaker "the card has no such value".
func TestHoldingsDocumentOmitsDetailWithoutAStoredDocument(t *testing.T) {
	out := write(t, FromExportRows([]export.Row{
		{Count: 1, Name: "Unfetched Card", Set: "xxx", CollectorNumber: "1",
			Finish: finish.Nonfoil, ScryfallID: "unf",
			Container: "Binder", Kind: "binder", Board: "main"},
	}))
	if strings.Contains(out, "detail") {
		t.Errorf("a printing with no stored document emitted a detail key:\n%s", out)
	}
}

// A hoard document must never carry detail, whatever the rows handed to it
// hold. Every printing in it embeds the same Scryfall document verbatim, so
// the derived copies would be redundant — and, decisively, `hoard merge`
// identifies a merge by hashing these bytes and refuses a source already in
// the ledger. A field added here moves every hash, so every ledger row stops
// matching and a re-merge doubles every quantity.
func TestHoardDocumentCarriesNoDetail(t *testing.T) {
	doc := FromSnapshot(store.Snapshot{Version: 27}, detailRows())
	out := write(t, doc)
	if strings.Contains(out, "detail") {
		t.Errorf("the interchange document carried card detail:\n%s", out)
	}
	for _, row := range doc.Hoard.Holdings.Rows {
		if row.Detail != nil {
			t.Errorf("row %q kept its detail in the hoard document", row.Card.Name)
		}
	}
}

// The other kinds share the Card type, and detail deliberately does not live
// there: a field on Card lands in eight kinds at once, growing five documents
// nobody asked to grow — or, worse, being declared in their schemas while
// their queries never fill it. Their documents must come back untouched.
func TestKindsSharingCardCarryNoDetail(t *testing.T) {
	docs := map[string]Document{
		"summary": FromSummary(store.CollectionTotals{}, nil),
		"unpriced": FromUnpriced([]store.UnpricedRow{{
			ScryfallID: "sol", Name: "Sol Ring", SetCode: "c21",
			CollectorNumber: "125", Finish: finish.Nonfoil, Copies: 1}}),
		"movers": FromMovers("2026-06-30T00:00:00Z", "", []store.PriceChange{{
			ScryfallID: "sol", Name: "Sol Ring", SetCode: "c21",
			CollectorNumber: "125", Finish: finish.Foil, Copies: 1, Old: 1, New: 2}}),
		"report": FromValuation(report.ValuationData{
			Top: []store.OwnedFinish{{ScryfallID: "sol", Name: "Sol Ring",
				SetCode: "c21", CollectorNumber: "125", Finish: finish.Foil,
				Copies: 1, Value: 2}}}),
		"watch": FromWatchCheck(1, []store.WatchStatus{{
			Watch: store.Watch{ScryfallID: "sol", Finish: finish.Foil, Op: "over"},
			Name:  "Sol Ring", SetCode: "c21", CollectorNumber: "125"}}),
		"market": FromMarket(market.Result{Compared: 1,
			Opportunities: []market.Opportunity{{
				Card: store.OwnedFinish{ScryfallID: "sol", Name: "Sol Ring",
					SetCode: "c21", CollectorNumber: "125", Finish: finish.Nonfoil,
					Copies: 1, Value: 2}}}}),
	}
	for kind, doc := range docs {
		if out := write(t, doc); strings.Contains(out, "detail") {
			t.Errorf("the %s document carried card detail:\n%s", kind, out)
		}
	}
}

// identity is what one kind says about one card's color: the decoded letters,
// and whether the key was there at all. Absent and `[]` are different answers
// — "not known to hoard" and "colorless" — so no assertion can ask this with
// len() alone.
type identity struct {
	letters []string
	present bool
}

// identitiesAt reads every card in one array of card-bearing objects, found by
// path because the kinds keep their cards in different places. The path is the
// argument rather than a search so a test names the array it means: a hoard
// document carries cards under both holdings and watches, and a search would
// answer about whichever came first.
func identitiesAt(t *testing.T, doc string, path ...string) map[string]identity {
	t.Helper()
	var tree any
	if err := json.Unmarshal([]byte(doc), &tree); err != nil {
		t.Fatalf("parsing the emitted document: %v", err)
	}
	at := tree
	for _, key := range path {
		obj, ok := at.(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object on the way to %v:\n%s", key, path, doc)
		}
		at = obj[key]
	}
	list, ok := at.([]any)
	if !ok {
		t.Fatalf("%v is not an array:\n%s", path, doc)
	}
	out := map[string]identity{}
	for _, e := range list {
		card, ok := e.(map[string]any)["card"].(map[string]any)
		if !ok {
			t.Fatalf("an entry under %v carries no card:\n%s", path, doc)
		}
		name, _ := card["name"].(string)
		raw, present := card["colorIdentity"]
		got := identity{present: present}
		if present {
			letters, ok := raw.([]any)
			if !ok {
				t.Fatalf("%s's colorIdentity is not an array:\n%s", name, doc)
			}
			got.letters = []string{}
			for _, l := range letters {
				got.letters = append(got.letters, l.(string))
			}
		}
		out[name] = got
	}
	return out
}

// identityWatches are the same three states colorIdentityRows describes, as
// the watch paths carry them: WatchStatus is what both the interchange
// document and the fired-alert document are built from.
func identityWatches() []store.WatchStatus {
	return []store.WatchStatus{
		{Watch: store.Watch{ScryfallID: "sol", Finish: finish.Nonfoil, Op: "over", Threshold: 1},
			Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
			ColorIdentity: []string{}, PriceUSD: f(2)},
		{Watch: store.Watch{ScryfallID: "swp", Finish: finish.Nonfoil, Op: "over", Threshold: 1},
			Name: "Swamp", SetCode: "c21", CollectorNumber: "300",
			ColorIdentity: []string{"B"}, PriceUSD: f(2)},
		{Watch: store.Watch{ScryfallID: "unf", Finish: finish.Nonfoil, Op: "over", Threshold: 1},
			Name: "Unfetched Card", SetCode: "xxx", CollectorNumber: "1", PriceUSD: f(2)},
	}
}

// identityTop is those three states as the report's biggest holdings carry
// them. OwnedFinish has always had the field; report.topHoldings simply did
// not read it.
func identityTop() []store.OwnedFinish {
	return []store.OwnedFinish{
		{ScryfallID: "sol", Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
			Finish: finish.Nonfoil, Copies: 1, Value: 2, ColorIdentity: []string{}},
		{ScryfallID: "swp", Name: "Swamp", SetCode: "c21", CollectorNumber: "300",
			Finish: finish.Nonfoil, Copies: 1, Value: 1, ColorIdentity: []string{"B"}},
		{ScryfallID: "unf", Name: "Unfetched Card", SetCode: "xxx", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 1, Value: 3},
	}
}

// One run tells one story. The kinds share the Card type and the schema that
// documents it, so a card that is `["B"]` in holdings and absent from
// topHoldings makes one document say hoard both knows and does not know the
// same fact — which is what the owner's database produced: Bitterblossom was
// ["B"] in holdings, absent from report.topHoldings, and ["B"] in the row the
// query read.
//
// Asserted across kinds rather than per kind because that is the property. A
// kind can only be checked against the schema's words; checked against another
// kind from the same run, a disagreement is a contradiction no reading excuses.
func TestEveryKindAgreesOnACardsIdentity(t *testing.T) {
	rows, watches := colorIdentityRows(), identityWatches()
	kinds := map[string]map[string]identity{
		"holdings": identitiesAt(t,
			write(t, FromExportRows(rows)), "holdings", "rows"),
		"report.topHoldings": identitiesAt(t,
			write(t, FromValuation(report.ValuationData{Top: identityTop()})),
			"report", "topHoldings"),
		"hoard.watches": identitiesAt(t,
			write(t, FromSnapshot(store.Snapshot{Version: 27, Watches: watches}, rows)),
			"hoard", "watches"),
		"watch.fired": identitiesAt(t,
			write(t, FromWatchCheck(len(watches), watches)), "watch", "fired"),
	}
	// Both states, because the bug is about telling them apart: a fix that
	// hard-coded a non-empty slice would satisfy the colored card alone.
	want := map[string]identity{
		"Swamp":          {letters: []string{"B"}, present: true},
		"Sol Ring":       {letters: []string{}, present: true},
		"Unfetched Card": {present: false},
	}
	for kind, cards := range kinds {
		for name, w := range want {
			got, ok := cards[name]
			if !ok {
				t.Fatalf("%s carries no card named %q", kind, name)
			}
			if got.present != w.present {
				t.Errorf("%s: %s colorIdentity present = %v, want %v — %q says the opposite",
					kind, name, got.present, w.present, "holdings")
				continue
			}
			if w.present && !reflect.DeepEqual(got.letters, w.letters) {
				t.Errorf("%s: %s colorIdentity = %v, want %v",
					kind, name, got.letters, w.letters)
			}
		}
	}
}

// A percentage is what the movers text view has always printed and the
// document has always left out, so every consumer recomputed it — and each one
// decided for itself what a rise from nothing means. This pins that hoard now
// decides that once.
func TestMoversDocumentCarriesThePercentage(t *testing.T) {
	got := write(t, FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
		[]store.PriceChange{
			{ScryfallID: "a", Name: "Cryptolith Rite", SetCode: "soi", CollectorNumber: "200",
				Finish: finish.Nonfoil, Copies: 3, Old: 6.31, New: 13.54, Source: "scryfall",
				OldAsOf: "2026-06-30T00:00:00Z"},
		}))
	// +114.6% is what the table prints for this row; the document carries the
	// quotient whole, so a consumer formatting it to a tenth of a percent lands
	// on the same figure the table shows rather than beside it.
	if !strings.Contains(got, `"pctChange": 1.1458003169572109`) {
		t.Errorf("movers document has no pctChange for a normal row:\n%s", got)
	}
}

// The case every consumer had to decide alone, which is the whole reason the
// field is worth carrying: a printing with no price at the start of the window
// has no percentage, only an infinite one.
//
// Absent rather than null or 0. Null cannot be spelled here — a field without
// omitempty is REQUIRED and non-nullable in the generated schema, so a document
// carrying one would fail hoard's own published contract — and 0 is the value a
// printing that did not move would carry, which is the opposite claim.
func TestMoversDocumentOmitsThePercentageOfARiseFromNothing(t *testing.T) {
	got := write(t, FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
		[]store.PriceChange{
			{ScryfallID: "b", Name: "Barrowgoyf", SetCode: "mh3", CollectorNumber: "185",
				Finish: finish.Foil, Copies: 1, Old: 0, New: 12, Source: "scryfall",
				OldAsOf: "2026-07-01T09:00:00Z"},
		}))
	if strings.Contains(got, "pctChange") {
		t.Errorf("a rise from $0 carries a percentage; it has none:\n%s", got)
	}
	// The row itself is still reported — omitting the field is not omitting
	// the change.
	if !strings.Contains(got, `"newUsd": 12`) {
		t.Errorf("the change itself was dropped along with its percentage:\n%s", got)
	}
}

// The document's figure comes from the accessor the CHANGE column renders,
// not from a second (newUsd-oldUsd)/oldUsd written beside it. A reimplementation
// would agree on every ordinary row and diverge on exactly the rows that matter,
// so this checks agreement across a spread of them including the undefined one.
func TestMoversPercentageTracksTheStoreAccessor(t *testing.T) {
	changes := []store.PriceChange{
		{ScryfallID: "a", Name: "Up", Finish: finish.Nonfoil, Copies: 1, Old: 6.31, New: 13.54},
		{ScryfallID: "b", Name: "Down", Finish: finish.Nonfoil, Copies: 1, Old: 2, New: 1.5},
		{ScryfallID: "c", Name: "Tiny", Finish: finish.Nonfoil, Copies: 1, Old: 0.02, New: 0.03},
		{ScryfallID: "d", Name: "FromZero", Finish: finish.Nonfoil, Copies: 1, Old: 0, New: 5},
	}
	doc := FromMovers("2026-06-30T00:00:00Z", "", changes)
	byID := map[string]PriceChange{}
	for _, c := range doc.Movers.Changes {
		byID[c.Card.ScryfallID] = c
	}
	for _, c := range changes {
		got := byID[c.ScryfallID]
		if !c.PctDefined() {
			if got.PctChange != nil {
				t.Errorf("%s: pctChange = %v, want absent", c.Name, *got.PctChange)
			}
			continue
		}
		if got.PctChange == nil {
			t.Fatalf("%s: pctChange absent, want %v", c.Name, c.Pct())
		}
		// Bit-identical to the accessor, not merely close to it: the point of
		// the field is that there is one computation, and a second one written
		// beside it would agree to several decimal places.
		if *got.PctChange != c.Pct() {
			t.Errorf("%s: pctChange = %v, want %v — the document is not reading store.Pct",
				c.Name, *got.PctChange, c.Pct())
		}
	}
}

// A binder's id is the argument --binder takes on export, import and add. The
// document exists so that discovering one does not mean parsing a table.
func TestBindersDocumentCarriesIDs(t *testing.T) {
	got := write(t, FromBinders([]store.DeckSummary{
		{Container: store.Container{ID: 1, Name: "Binder"}, IsDefault: true,
			DistinctCards: 679, TotalCopies: 915, Value: 3797.1899999999996},
		{Container: store.Container{ID: 4, Name: "Trade Stock"},
			DistinctCards: 12, TotalCopies: 30, Value: 61.5},
	}))
	want := `{
  "schemaVersion": "1.2.1",
  "kind": "binders",
  "binders": {
    "rows": [
      {
        "id": 1,
        "name": "Binder",
        "isDefault": true,
        "distinctCards": 679,
        "totalCopies": 915,
        "valueUsd": 3797.19
      },
      {
        "id": 4,
        "name": "Trade Stock",
        "isDefault": false,
        "distinctCards": 12,
        "totalCopies": 30,
        "valueUsd": 61.5
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("binders document:\n%s\nwant:\n%s", got, want)
	}
}

// An empty list is [] and not null: null tells a consumer the field is missing,
// which for a hoard whose binders were all removed is a different claim.
func TestBindersDocumentEmitsAnEmptyList(t *testing.T) {
	got := write(t, FromBinders(nil))
	if !strings.Contains(got, `"rows": []`) {
		t.Errorf("no binders emitted null rather than an empty list:\n%s", got)
	}
}

// The guessed queue is a worklist keyed by row id, and its rows are per commit:
// two copies of one printing scanned on the same default are two rows identical
// in every field but the id, and both are cards somebody has to pick up. A
// converter that deduplicated them would lose one of the two.
func TestGuessedDocumentKeepsRowsWithIdenticalCards(t *testing.T) {
	got := write(t, FromGuessed([]store.FinishGuessRow{
		{ID: 13, ScryfallID: "whisperer", Name: "Primal Whisperer", Set: "lgn",
			Number: "135", Finish: finish.Nonfoil, GuessedAt: "2026-08-09T18:20:00Z"},
		{ID: 12, ScryfallID: "whisperer", Name: "Primal Whisperer", Set: "lgn",
			Number: "135", Finish: finish.Nonfoil, GuessedAt: "2026-08-09T18:20:00Z"},
	}))
	want := `{
  "schemaVersion": "1.2.1",
  "kind": "guessed",
  "guessed": {
    "rows": [
      {
        "id": 13,
        "card": {
          "name": "Primal Whisperer",
          "scryfallId": "whisperer",
          "setCode": "lgn",
          "number": "135",
          "finish": "nonfoil"
        },
        "guessedAt": "2026-08-09T18:20:00Z"
      },
      {
        "id": 12,
        "card": {
          "name": "Primal Whisperer",
          "scryfallId": "whisperer",
          "setCode": "lgn",
          "number": "135",
          "finish": "nonfoil"
        },
        "guessedAt": "2026-08-09T18:20:00Z"
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("guessed document:\n%s\nwant:\n%s", got, want)
	}
}

// An emptied queue is the answer a script working the list down is waiting for,
// so it is a document with no rows rather than a sentence about there being
// none.
func TestGuessedDocumentEmitsAnEmptyList(t *testing.T) {
	got := write(t, FromGuessed(nil))
	if !strings.Contains(got, `"rows": []`) {
		t.Errorf("an empty queue emitted null rather than an empty list:\n%s", got)
	}
}
