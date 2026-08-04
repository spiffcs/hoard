package watchsource

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The CSV dialect: one header row, cells looked up by header name — never by
// position — so a reordered or extended file still parses. Column names match
// hoard's canonical collection export where the two overlap, so a generator
// that already speaks one dialect speaks both.
const (
	colName     = "Name"
	colDir      = "Direction"
	colThresh   = "Threshold"
	colFinish   = "Finish"
	colSet      = "Set"
	colNumber   = "Collector Number"
	colScryfall = "Scryfall ID"
)

func parseCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	// Ragged rows are tolerated and cells beyond a row's end read as empty:
	// generators routinely omit trailing empty fields.
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("empty file")
	}

	header := records[0]
	// Excel and some apps prefix UTF-8 exports with a byte-order mark.
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	cols := make(map[string]int, len(header))
	for i, h := range header {
		cols[strings.ToLower(strings.TrimSpace(h))] = i
	}
	col := func(name string) int {
		if i, ok := cols[strings.ToLower(name)]; ok {
			return i
		}
		return -1
	}
	get := func(rec []string, name string) string {
		if i := col(name); i >= 0 && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	for _, required := range []string{colName, colDir, colThresh} {
		if col(required) < 0 {
			return nil, fmt.Errorf("watch CSV is missing its %q column (saw: %s)",
				required, strings.Join(header, ", "))
		}
	}

	var out []Row
	for n, rec := range records[1:] {
		line := n + 2 // 1-based, counting the header
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		name := get(rec, colName)
		if name == "" {
			return nil, fmt.Errorf("line %d: no card name", line)
		}
		op, err := normDirection(get(rec, colDir))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", line, name, err)
		}
		threshold, err := parseThreshold(get(rec, colThresh))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", line, name, err)
		}

		out = append(out, Row{
			Ident:     identFor(get(rec, colScryfall), get(rec, colSet), get(rec, colNumber), name),
			Name:      name,
			Finish:    normFinish(get(rec, colFinish)),
			Op:        op,
			Threshold: threshold,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("no watches found in file")
	}
	return out, nil
}
