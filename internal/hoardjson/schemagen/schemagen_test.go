package schemagen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	santhosh "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func schemaPath(name string) string {
	return filepath.Join(RepoRoot(), "schema", "json", name)
}

// TestSchemaFilesMatchModel is the drift check: the committed schema files
// must be byte-for-byte what the model generates. When it fails, run
// `make generate-json-schema` — and if schema-<version>.json has already been
// released, bump hoardjson.SchemaVersion first: released files are immutable.
func TestSchemaFilesMatchModel(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, name := range []string{"schema-latest.json", "schema-" + hoardjson.SchemaVersion + ".json"} {
		onDisk, err := os.ReadFile(schemaPath(name))
		if err != nil {
			t.Fatalf("reading %s: %v (run `make generate-json-schema`)", name, err)
		}
		if !bytes.Equal(onDisk, generated) {
			t.Errorf("%s does not match the model in internal/hoardjson.\n"+
				"Run `make generate-json-schema`; if this schema version was already "+
				"released, bump hoardjson.SchemaVersion first.", name)
		}
	}
}

// compileSchema loads the committed latest schema into a validator.
func compileSchema(t *testing.T) *santhosh.Schema {
	t.Helper()
	raw, err := os.ReadFile(schemaPath("schema-latest.json"))
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	doc, err := santhosh.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	c := santhosh.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		t.Fatalf("adding schema resource: %v", err)
	}
	sch, err := c.Compile("schema.json")
	if err != nil {
		t.Fatalf("compiling schema: %v", err)
	}
	return sch
}

func validate(t *testing.T, sch *santhosh.Schema, doc hoardjson.Document) error {
	t.Helper()
	var buf bytes.Buffer
	if err := hoardjson.Write(&buf, doc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	inst, err := santhosh.UnmarshalJSON(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parsing emitted document: %v", err)
	}
	return sch.Validate(inst)
}

func f(v float64) *float64 { return &v }

// TestEmittedDocumentsValidate closes the loop the generator opens: one
// representative document per kind, exercising both sides of every optional
// field, must satisfy the committed schema.
func TestEmittedDocumentsValidate(t *testing.T) {
	sch := compileSchema(t)

	docs := map[string]hoardjson.Document{
		"summary": hoardjson.FromSummary(
			store.CollectionTotals{DistinctCards: 2, TotalCopies: 3, Value: 14.5},
			[]store.DeckSummary{{Container: store.Container{Name: "Fish"},
				DistinctCards: 1, TotalCopies: 1, Value: 2}}),
		"holdings": hoardjson.FromExportRows([]export.Row{
			{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
				Finish: finish.Nonfoil, ScryfallID: "sol", MTGJSONUUID: "uu-sol",
				Container: "Binder", Kind: "binder", Board: "main", PriceUSD: f(2)},
			{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78",
				Finish: finish.Etched, Condition: "lp", ScryfallID: "rem",
				Container: "Fish", Kind: "deck", Board: "side"},
		}),
		"unpriced": hoardjson.FromUnpriced([]store.UnpricedRow{{
			ScryfallID: "rem", Name: "Mystic Remora", SetCode: "ice",
			CollectorNumber: "78", Finish: finish.Nonfoil, Copies: 3,
			Containers: []string{"Binder"}, HeldIn: "Binder"}}),
		"movers": hoardjson.FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
			[]store.PriceChange{{ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma",
				CollectorNumber: "236", Finish: finish.Foil, Copies: 1, Old: 30, New: 32,
				Source: "scryfall"}}),
		"movers-empty": hoardjson.FromMovers("2026-06-30T00:00:00Z", "", nil),
		// The other side of pctChange: a printing that was unpriced at the
		// window's start has no percentage, and the field is absent rather
		// than null or zero.
		"movers-from-zero": hoardjson.FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
			[]store.PriceChange{{ScryfallID: "b", Name: "Black Lotus", SetCode: "lea",
				CollectorNumber: "232", Finish: finish.Nonfoil, Copies: 1, Old: 0, New: 5,
				Source: "scryfall"}}),
		"watch": hoardjson.FromWatchCheck(2, []store.WatchStatus{{
			Watch: store.Watch{ScryfallID: "sol", Finish: finish.Foil, Op: "over", Threshold: 10},
			Name:  "Sol Ring", SetCode: "c21", CollectorNumber: "125", PriceUSD: f(12.5),
		}}),
		"watch-quiet": hoardjson.FromWatchCheck(0, nil),
		// Both sides of every optional field: an absolute watch with a price
		// and no anchor, and a movement with an anchor, a fire moment and no
		// price at all.
		"watches": hoardjson.FromWatchList([]store.WatchStatus{{
			Watch: store.Watch{ID: 1, ScryfallID: "sol", Display: "Sol Ring",
				Finish: finish.Foil, Op: "over", Threshold: 10, WindowDays: 30,
				CreatedAt: "2026-08-09T00:00:00Z", LastState: "met",
				LastFiredAt: "2026-08-10T00:00:00Z"},
			Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125", PriceUSD: f(12.5),
		}, {
			Watch: store.Watch{ID: 2, ScryfallID: "rem", Display: "Mystic Remora",
				Finish: finish.Nonfoil, Op: "drop", Pct: 0.1, MinMove: 0.25, WindowDays: 30},
			Name: "Mystic Remora", SetCode: "ice", CollectorNumber: "78",
		}}),
		"watches-empty": hoardjson.FromWatchList(nil),
		"report": hoardjson.FromValuation(report.ValuationData{
			AsOf:   "2026-07-30T09:00:00Z",
			Binder: store.CollectionTotals{DistinctCards: 2, TotalCopies: 3, Value: 16.5},
			Binders: []store.DeckSummary{{Container: store.Container{Name: "Binder"},
				DistinctCards: 2, TotalCopies: 3, Value: 16.5}},
			Decks: []store.DeckSummary{{Container: store.Container{Name: "Fish"},
				DistinctCards: 1, TotalCopies: 1, Value: 0}},
			Top: []store.OwnedFinish{{ScryfallID: "sol", Name: "Sol Ring", SetCode: "c21",
				CollectorNumber: "125", Finish: finish.Foil, Copies: 1, Value: 12.5}},
			Sources:  []store.SourceCount{{Source: "scryfall", Printings: 2, Copies: 3}},
			Unpriced: store.SourceCount{Printings: 1, Copies: 1},
		}),
		// The interchange document `hoard merge` moves between two databases.
		// The card document in Raw is the field most at risk here: the schema
		// declares it an object, and json.RawMessage would otherwise reflect
		// as a base64 string.
		"hoard": hoardjson.FromSnapshot(
			store.Snapshot{
				Version: 27,
				Printings: []store.SourcePrinting{{
					Card: scryfall.Card{ID: "sol", Name: "Sol Ring", Set: "c21",
						CollectorNumber: "125", ScryfallURL: "https://scryfall.com/card/c21/125",
						PriceUSD: f(2),
						Raw:      []byte(`{"rarity":"uncommon","type_line":"Artifact"}`)},
					MTGJSONUUID: "uu-sol", UpdatedAt: "2026-08-09T00:00:00Z",
				}, {
					// The other side of every optional field: no uuid, no
					// prices, no stored document.
					Card: scryfall.Card{ID: "rem", Name: "Mystic Remora", Set: "ice",
						CollectorNumber: "78", ScryfallURL: "https://scryfall.com/card/ice/78"},
					UpdatedAt: "2026-08-09T00:00:00Z",
				}},
				Containers: []store.Container{
					{Kind: store.KindCollection, Name: "Binder", Source: "manual",
						SourceID: "__collection__"},
					{Kind: store.KindDeck, Name: "Fish", Source: "archidekt",
						SourceID: "42", SourceURL: "https://archidekt.com/decks/42",
						Format: "commander"},
				},
				Watches: []store.WatchStatus{{
					Watch: store.Watch{ScryfallID: "sol", Display: "Sol Ring",
						Finish: finish.Foil, Op: "over", Threshold: 10,
						CreatedAt: "2026-08-09T00:00:00Z"},
					Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
				}},
			},
			[]export.Row{
				{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
					Finish: finish.Nonfoil, ScryfallID: "sol", MTGJSONUUID: "uu-sol",
					Container: "Binder", Kind: "binder", Board: "main", PriceUSD: f(2)},
				{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78",
					Finish: finish.Etched, Condition: "lp", ScryfallID: "rem",
					Container: "Fish", Kind: "deck", Board: "side"},
			}),
		"arbitrage": hoardjson.FromMarket(market.Result{
			Compared: 1,
			Opportunities: []market.Opportunity{{
				Card: store.OwnedFinish{ScryfallID: "a", Name: "Ancient Tomb",
					SetCode: "uma", CollectorNumber: "236", Finish: finish.Nonfoil,
					Copies: 1, Value: 60},
				Market: 8, BuyAt: 4, BuyFrom: "cardmarket",
				SellAt: 5, SellTo: "cardkingdom",
				HasMarket: true, HasRetail: true, HasBuy: true}}}),
	}
	for name, doc := range docs {
		if err := validate(t, sch, doc); err != nil {
			t.Errorf("%s document does not validate against schema-latest.json:\n%v", name, err)
		}
	}
}

// TestSchemaRejectsUndeclaredFields proves additionalProperties actually
// bites: a document that grew a field the schema does not know must fail, or
// the drift check is the only thing standing between the schema and reality.
func TestSchemaRejectsUndeclaredFields(t *testing.T) {
	sch := compileSchema(t)
	inst, err := santhosh.UnmarshalJSON(bytes.NewReader([]byte(
		`{"schemaVersion": "1.0.0", "kind": "summary", "surprise": true}`)))
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Error("a document with an undeclared field validated; additionalProperties is not enforced")
	}
}
