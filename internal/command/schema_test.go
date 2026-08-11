package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	santhosh "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/schema"
)

// runSchemaCmd dispatches `hoard schema ...` through the real tree with no
// store, which is the point of the NoStore annotation: the schema is a fact
// about the build, so asking for it must not open — or create — a database.
func runSchemaCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return execCmd(context.Background(), nil, append([]string{"schema"}, args...), false)
}

// compile is the check that a slice is a schema rather than an excerpt: a
// compiler resolves every $ref, so a walk that kept Holdings and dropped the
// Holding it points at fails here rather than in whatever consumed the output.
func compile(t *testing.T, raw []byte) *santhosh.Schema {
	t.Helper()
	doc, err := santhosh.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	c := santhosh.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		t.Fatalf("adding resource: %v", err)
	}
	sch, err := c.Compile("schema.json")
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	return sch
}

// The unsliced output is the published file byte for byte, so a consumer can
// diff what the binary printed against the URL in its own $id and get nothing.
// Re-marshalling would reorder every object and break that.
func TestSchemaPrintsTheEmbeddedFileVerbatim(t *testing.T) {
	out, err := runSchemaCmd(t)
	if err != nil {
		t.Fatalf("hoard schema: %v", err)
	}
	if out != string(schema.Latest) {
		t.Errorf("output is not the embedded file verbatim (%d bytes vs %d)",
			len(out), len(schema.Latest))
	}
	// The embed points at schema-latest.json, which the drift test in
	// internal/hoardjson/schemagen holds to the model. Checking the $id here
	// catches the one thing that test cannot: an embed aimed at some other
	// file in the same directory, every one of which is also a valid schema.
	if want := "schema-" + hoardjson.SchemaVersion + ".json"; !strings.Contains(out, want) {
		t.Errorf("embedded schema's $id does not name %s; the go:embed is aimed at the wrong file", want)
	}
}

// --kind is the reason the command exists: the whole schema is 22 KB and a
// single kind is a few, which is the difference between a cheap prompt and an
// expensive one. It has to be a real reachability walk — dropping the $defs a
// kind does not touch — not a smaller header on the same 22 KB.
func TestSchemaKindKeepsOnlyWhatThatKindReaches(t *testing.T) {
	full, err := runSchemaCmd(t)
	if err != nil {
		t.Fatalf("hoard schema: %v", err)
	}
	fullDefs := defNames(t, []byte(full))

	for _, kind := range []string{
		"summary", "holdings", "unpriced", "movers", "market", "report", "watch", "hoard",
		"binders", "guessed",
	} {
		t.Run(kind, func(t *testing.T) {
			out, err := runSchemaCmd(t, "--kind", kind)
			if err != nil {
				t.Fatalf("hoard schema --kind %s: %v", kind, err)
			}
			compile(t, []byte(out))

			if len(out) >= len(full) {
				t.Errorf("slice is %d bytes against the full schema's %d — nothing was dropped",
					len(out), len(full))
			}

			var doc map[string]any
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("parsing slice: %v", err)
			}
			props, _ := doc["properties"].(map[string]any)
			for _, want := range []string{"schemaVersion", "kind", kind} {
				if _, ok := props[want]; !ok {
					t.Errorf("slice dropped the %q property", want)
				}
			}
			if len(props) != 3 {
				t.Errorf("slice carries %d properties, want the envelope plus %q: %v",
					len(props), kind, propNames(props))
			}

			// The enum is narrowed to the one kind, so the slice rejects a
			// document of another kind — a distinction the full schema cannot
			// make, since it has to accept all eight.
			kindProp, _ := props["kind"].(map[string]any)
			enum, _ := kindProp["enum"].([]any)
			if len(enum) != 1 || enum[0] != kind {
				t.Errorf("kind enum = %v, want exactly [%q]", enum, kind)
			}

			got := defNames(t, []byte(out))
			if len(got) == 0 {
				t.Fatal("slice has no $defs at all")
			}
			if len(got) >= len(fullDefs) {
				t.Errorf("slice kept %d of %d definitions — the walk is not narrowing anything",
					len(got), len(fullDefs))
			}
			for name := range got {
				if !fullDefs[name] {
					t.Errorf("slice invented a definition %q", name)
				}
			}
		})
	}
}

// The walk has to be transitive. Holdings names Holding, which names Card and
// Container; keeping only the directly-referenced definition would leave two
// dangling pointers, and dropping an unrelated branch is what the flag is for.
func TestSchemaKindWalksDefinitionsTransitively(t *testing.T) {
	out, err := runSchemaCmd(t, "--kind", "holdings")
	if err != nil {
		t.Fatalf("hoard schema --kind holdings: %v", err)
	}
	got := defNames(t, []byte(out))

	for _, want := range []string{"Holdings", "Holding", "Card"} {
		if !got[want] {
			t.Errorf("slice is missing %q, which holdings reaches; it kept %v", want, got)
		}
	}
	for _, unwanted := range []string{"Movers", "PriceChange", "Report", "Market", "Opportunity"} {
		if got[unwanted] {
			t.Errorf("slice kept %q, which holdings does not reach", unwanted)
		}
	}
}

// A slice is only worth sending if it still validates the thing it describes.
func TestSchemaKindValidatesADocumentOfThatKind(t *testing.T) {
	out, err := runSchemaCmd(t, "--kind", "holdings")
	if err != nil {
		t.Fatalf("hoard schema --kind holdings: %v", err)
	}
	sch := compile(t, []byte(out))

	price := 2.0
	doc := hoardjson.FromExportRows([]export.Row{
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
			Finish: "nonfoil", ScryfallID: "sol", MTGJSONUUID: "uu-sol",
			Container: "Binder", Kind: "binder", Board: "main", PriceUSD: &price},
	})
	var buf bytes.Buffer
	if err := hoardjson.Write(&buf, doc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	inst, err := santhosh.UnmarshalJSON(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parsing emitted document: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("a real holdings document does not validate against its own slice:\n%v", err)
	}
}

// A typo'd kind is the user's to fix, so it is a usage error and it says what
// the kinds are — read out of the schema's own enum, so the list cannot fall
// behind hoardjson.Document.
func TestSchemaRejectsAnUnknownKindWithTheList(t *testing.T) {
	_, err := runSchemaCmd(t, "--kind", "holding")
	if err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
	if !errors.Is(err, cli.ErrUsage) {
		t.Errorf("rejection is not ErrUsage: %v", err)
	}
	for _, want := range []string{`"holding"`, "holdings", "movers", "hoard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err, want)
		}
	}
}

func defNames(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing $defs: %v", err)
	}
	names := make(map[string]bool, len(doc.Defs))
	for name := range doc.Defs {
		names[name] = true
	}
	return names
}

func propNames(m map[string]any) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}
