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
	// Movement columns. watchsource looks cells up by header name and never
	// by position, so these are additive by construction: a file written
	// before they existed parses unchanged.
	colPercent = "Percent"
	colMinMove = "Min Move"
	colSince   = "Since"
)

func parseCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	// Go's ErrFieldCount is off because ragged rows are only half-legitimate,
	// and the excuse runs one way. A row *shorter* than the header is
	// tolerated and its missing cells read as empty: generators routinely omit
	// trailing empty fields. A row *longer* than the header cannot be that —
	// it can only be a delimiter that should have been quoted, nearly always a
	// comma inside a card name. Every column past it shifts by one, so a
	// fragment of the name arrives where the direction or threshold belongs.
	// Over-long rows are refused in the loop below, by line number, before
	// anything is resolved. Same reasoning as collsource; same trap.
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

	for _, required := range []string{colName, colDir} {
		if col(required) < 0 {
			return nil, fmt.Errorf("watch CSV is missing its %q column (saw: %s)",
				required, strings.Join(header, ", "))
		}
	}
	// One of the two size columns has to be there. Which one is a per-row
	// question — a file may carry both columns and fill one per row — but a
	// file with neither cannot state a single watch, and saying so once about
	// the header beats saying it about every line.
	if col(colThresh) < 0 && col(colPercent) < 0 {
		return nil, fmt.Errorf("watch CSV needs a %q or %q column (saw: %s)",
			colThresh, colPercent, strings.Join(header, ", "))
	}

	var out []Row
	for n, rec := range records[1:] {
		line := n + 2 // 1-based, counting the header
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		// Refused before the name is even read: with the columns shifted,
		// every cell this row offers belongs to the wrong header, and the
		// ones that still look plausible are the dangerous ones.
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
