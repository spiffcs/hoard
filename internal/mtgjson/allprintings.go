package mtgjson

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"github.com/spiffcs/hoard/internal/boundedio"
)

const allPrintingsFile = "AllPrintings.json.gz"

func AllIdentifiers(ctx context.Context, o Options) (map[string]SetCard, error) {
	body, err := fetch(ctx, o, allPrintingsFile)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", allPrintingsFile, err)
	}
	defer body.Close()

	bc := &boundedio.Counter{R: body}
	zr, err := gzip.NewReader(bc)
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", allPrintingsFile, err)
	}
	defer zr.Close()
	bounded := boundedio.LimitRatio(zr, bc, "the printings file")

	dec := json.NewDecoder(bounded)
	if err := seekToData(dec); err != nil {
		return nil, err
	}

	out := map[string]SetCard{}
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		code, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", allPrintingsFile, err)
		}
		var set setData
		if err := dec.Decode(&set); err != nil {
			return nil, fmt.Errorf("decoding set %v in %s: %w", code, allPrintingsFile, err)
		}
		addSetCards(out, set.Cards)
	}
	return out, nil
}

func seekToData(dec *json.Decoder) error {
	open, err := dec.Token()
	if err != nil {
		return fmt.Errorf("reading %s: %w", allPrintingsFile, err)
	}
	if d, ok := open.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("%s is not a JSON object", allPrintingsFile)
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading %s: %w", allPrintingsFile, err)
		}
		if key != "data" {
			if err := skipValue(dec); err != nil {
				return fmt.Errorf("reading %s: %w", allPrintingsFile, err)
			}
			continue
		}
		sets, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading %s: %w", allPrintingsFile, err)
		}
		if d, ok := sets.(json.Delim); !ok || d != '{' {
			return fmt.Errorf("%s has a non-object data section", allPrintingsFile)
		}
		return nil
	}
	return fmt.Errorf("%s has no data section", allPrintingsFile)
}

func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}
