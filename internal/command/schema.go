package command

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

const defsPrefix = "#/$defs/"

func NewCmdSchema(a *app) *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:     "schema",
		GroupID: groupInterop,
		Short:   "The JSON Schema that this build's --json output follows",

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

	_, err := env.Out.Write(out)
	return err
}

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

	kinds, err := documentKinds(props)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(kinds, kind) {
		return nil, cli.Usagef("unknown kind %q (want %s)", kind, strings.Join(kinds, ", "))
	}

	payload, ok := props[kind]
	if !ok {
		return nil, fmt.Errorf("schema declares kind %q with no matching property", kind)
	}
	kept := map[string]any{
		"schemaVersion": props["schemaVersion"],

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

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func decodeSchema(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing the embedded schema: %w", err)
	}
	return doc, nil
}

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
