package schemagen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	santhosh "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
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
				Finish: "nonfoil", ScryfallID: "sol", MTGJSONUUID: "uu-sol",
				Container: "Binder", Kind: "binder", Board: "main", PriceUSD: f(2)},
			{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78",
				Finish: "etched", ScryfallID: "rem", Container: "Fish", Kind: "deck", Board: "side"},
		}),
		"unpriced": hoardjson.FromUnpriced([]store.UnpricedRow{{
			ScryfallID: "rem", Name: "Mystic Remora", SetCode: "ice",
			CollectorNumber: "78", Finish: "nonfoil", Copies: 3,
			Containers: []string{"Binder"}, HeldIn: "Binder"}}),
		"movers": hoardjson.FromMovers("2026-06-30T00:00:00Z", "2026-07-01T09:00:00Z",
			[]store.PriceChange{{ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma",
				CollectorNumber: "236", Finish: "foil", Copies: 1, Old: 30, New: 32,
				Source: "scryfall"}}),
		"movers-empty": hoardjson.FromMovers("2026-06-30T00:00:00Z", "", nil),
		"arbitrage": hoardjson.FromArbitrage(arbitrage.Result{
			Compared: 1, Ignored: 0,
			Opportunities: []arbitrage.Opportunity{{
				Card: store.OwnedFinish{ScryfallID: "a", Name: "Ancient Tomb",
					SetCode: "uma", CollectorNumber: "236", Finish: "nonfoil",
					Copies: 1, Value: 60},
				BuyAt: 4, BuyFrom: "tcgplayer", DearAt: 6, DearFrom: "cardmarket",
				SellAt: 5, SellTo: "cardkingdom", HasRetail: true, HasBuy: true}}}),
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
