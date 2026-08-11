package command

// The schema command: the JSON Schema this build's --json output follows,
// whole or narrowed to the definitions one document kind reaches.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/schema"
)

// defsPrefix is the only $ref shape schemagen emits — every reference in the
// document schema is a local pointer into $defs, which is what makes the
// reachability walk below a closed question rather than a fetch.
const defsPrefix = "#/$defs/"

// NewCmdSchema builds `hoard schema`.
//
// --json is accepted rather than refused, which is why this carries
// AnnotationJSON despite ignoring Env.JSON. CheckJSON exists to stop a script
// that asked for JSON being handed a table, and this command has no table to
// hand it; refusing would answer "hoard schema has no JSON output" to the one
// command whose entire output is JSON.
//
// NoStore because the schema is a property of the build, not of a collection:
// asking what shape hoard's output takes must not create a database as a side
// effect of the asking.
func NewCmdSchema(a *app) *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:     "schema",
		GroupID: groupInterop,
		Short:   "The JSON Schema that this build's --json output follows",
		// Wrapped to 60 columns, the narrowest terminal hoard's help claims to
		// render in. writeCommandHelp copies Long through verbatim, so a Long
		// is only ever as narrow as it was written; this one shipped at 75 and
		// was found by the tree-wide sweep in usage_test.go, not by eye.
		Long: "The JSON Schema that this build's --json output follows,\n" +
			"printed to stdout.\n\n" +
			"It is the contract rather than the collection: handed to\n" +
			"a model or a validator, it is enough to write a correct\n" +
			"query against hoard's JSON without any card data leaving\n" +
			"the machine. --kind narrows it to one document kind — the\n" +
			"envelope, that kind's payload, and the definitions they\n" +
			"reach — which is a few KB instead of twenty.",
		Example: "hoard schema\n" +
			"hoard schema --kind holdings",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runSchema(a.env, kind)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "",
		"only what one document kind needs (summary, holdings, unpriced, movers, market, report, watch, watches, hoard)")
	return cli.NoStore(cli.JSONCapable(cmd))
}

func runSchema(env *cli.Env, kind string) error {
	out := schema.Latest
	if kind != "" {
		var err error
		if out, err = sliceSchema(schema.Latest, kind); err != nil {
			return err
		}
	}
	// The whole schema goes out as the embedded bytes, not re-marshalled: it is
	// byte-for-byte the file the $id names, and a round trip through map[string]any
	// would reorder every object and hand back something a consumer diffing
	// against the published file would see as different.
	_, err := env.Out.Write(out)
	return err
}

// sliceSchema narrows the document schema to one kind: the envelope, that
// kind's payload property, and the transitive closure of the $defs those two
// reach.
//
// Size is the whole point of the flag. The full schema is 22 KB and a kind's
// slice is 3-8 KB, which for the use this command exists for — sending a model
// the contract so it can write a correct query locally — is the difference
// between a cheap prompt and an expensive one.
//
// The result is still a schema, not an excerpt: it keeps $schema, $id and
// additionalProperties, so it compiles and validates documents of that kind on
// its own. Keys come out sorted rather than in the generator's order, which is
// what marshalling a map costs; it is stable, and only the unsliced output has
// to match the published file exactly.
func sliceSchema(raw []byte, kind string) ([]byte, error) {
	doc, err := decodeSchema(raw)
	if err != nil {
		return nil, err
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded schema has no properties object")
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded schema has no $defs object")
	}

	// The kinds are read out of the schema's own enum rather than restated
	// here, so a kind added to hoardjson.Document is sliceable the moment the
	// schema is regenerated — this command cannot fall behind the model.
	kinds, err := documentKinds(props)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(kinds, kind) {
		return nil, cli.Usagef("unknown kind %q (want %s)", kind, strings.Join(kinds, ", "))
	}

	// A kind names its payload property: hoardjson.Document tags each pointer
	// with the same string the Kind constant carries.
	payload, ok := props[kind]
	if !ok {
		return nil, fmt.Errorf("schema declares kind %q with no matching property", kind)
	}
	kept := map[string]any{
		"schemaVersion": props["schemaVersion"],
		// Narrowed to the one value, so the slice validates the kind it
		// describes and rejects a document of any other — the whole schema
		// cannot make that distinction, since it has to accept all eight.
		"kind": map[string]any{"type": "string", "enum": []any{kind}},
		kind:   payload,
	}

	reachable, err := reachableDefs(payload, defs)
	if err != nil {
		return nil, err
	}
	sliced := make(map[string]any, len(reachable))
	for name := range reachable {
		sliced[name] = defs[name]
	}

	doc["properties"] = kept
	doc["$defs"] = sliced
	// "required" is left as the generator wrote it — schemaVersion and kind.
	// Adding the payload would claim hoard never emits a kind whose body is
	// absent, which is a promise about every command's output rather than a
	// fact this function can check.

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// decodeSchema reads the schema into generic maps with numbers left as their
// literal text. Plain unmarshalling turns every number into a float64, which
// re-marshals a threshold of 1000000 as 1e+06 — valid JSON that no longer
// reads like the schema anyone published.
func decodeSchema(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing the embedded schema: %w", err)
	}
	return doc, nil
}

// documentKinds reads the kind enum, in the order the generator wrote it, so
// the error text for a bad --kind lists them the way the schema does.
func documentKinds(props map[string]any) ([]string, error) {
	kindProp, ok := props["kind"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded schema has no kind property")
	}
	enum, ok := kindProp["enum"].([]any)
	if !ok {
		return nil, fmt.Errorf("embedded schema's kind property has no enum")
	}
	kinds := make([]string, 0, len(enum))
	for _, v := range enum {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("embedded schema's kind enum holds a non-string")
		}
		kinds = append(kinds, s)
	}
	return kinds, nil
}

// reachableDefs walks $refs from one payload until nothing new turns up.
//
// A closure, not one hop: Holdings names Holding, which names Card and
// Container. Keeping only the directly-referenced definitions would emit a
// schema with dangling pointers, which is worse than the full 22 KB because it
// fails at compile time in the consumer rather than costing tokens.
func reachableDefs(payload any, defs map[string]any) (map[string]bool, error) {
	reached := map[string]bool{}
	queue := collectRefs(payload, nil)
	for _, name := range queue {
		reached[name] = true
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		def, ok := defs[name]
		if !ok {
			return nil, fmt.Errorf("schema refers to a missing definition %q", name)
		}
		for _, next := range collectRefs(def, nil) {
			if !reached[next] {
				reached[next] = true
				queue = append(queue, next)
			}
		}
	}
	return reached, nil
}

// collectRefs appends the $defs names referenced anywhere inside v.
func collectRefs(v any, into []string) []string {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			if key == "$ref" {
				if s, ok := child.(string); ok && strings.HasPrefix(s, defsPrefix) {
					into = append(into, strings.TrimPrefix(s, defsPrefix))
				}
				continue
			}
			into = collectRefs(child, into)
		}
	case []any:
		for _, child := range t {
			into = collectRefs(child, into)
		}
	}
	return into
}
