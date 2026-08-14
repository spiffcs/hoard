package watchsource

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	colName     = "Name"
	colDir      = "Direction"
	colThresh   = "Threshold"
	colFinish   = "Finish"
	colSet      = "Set"
	colNumber   = "Collector Number"
	colScryfall = "Scryfall ID"

	colPercent = "Percent"
	colMinMove = "Min Move"
	colSince   = "Since"
)

func parseCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)

	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("empty file")
	}

	header := records[0]

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

	for _, required := range []string{colName, colDir} {
		if col(required) < 0 {
			return nil, fmt.Errorf("watch CSV is missing its %q column (saw: %s)",
				required, strings.Join(header, ", "))
		}
	}

	if col(colThresh) < 0 && col(colPercent) < 0 {
		return nil, fmt.Errorf("watch CSV needs a %q or %q column (saw: %s)",
			colThresh, colPercent, strings.Join(header, ", "))
	}

	var out []Row
	for n, rec := range records[1:] {
		line := n + 2
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}

		if len(rec) > len(header) {
			return nil, fmt.Errorf("line %d: %d fields, header has %d — an unquoted comma in a card name?",
				line, len(rec), len(header))
		}
		name := get(rec, colName)
		if name == "" {
			return nil, fmt.Errorf("line %d: no card name", line)
		}
		op, err := normDirection(get(rec, colDir))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", line, name, err)
		}
		row, err := units(op, get(rec, colThresh), get(rec, colPercent),
			get(rec, colMinMove), get(rec, colSince))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", line, name, err)
		}
		row.Ident = identFor(get(rec, colScryfall), get(rec, colSet), get(rec, colNumber), name)
		row.Name = name
		row.Finish = normFinish(get(rec, colFinish))
		row.Op = op
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, errors.New("no watches found in file")
	}
	return out, nil
}
