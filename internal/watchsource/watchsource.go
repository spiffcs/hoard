// Package watchsource parses watch-list files — CSV or JSON — into one
// normalized shape the watch import can resolve against Scryfall.
//
// The sibling of internal/collsource and internal/decksource: those parse
// what other tools export, this parses what other tools generate on purpose.
// A watch row carries a threshold and a direction instead of a quantity and
// a binder, which is why the shape is its own and not a collsource.Row.
package watchsource

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Row is one watch to stand.
//
// Ident carries the single best resolution scheme the file offered (Scryfall
// ID, else set+number, else name); Name is kept alongside even then, so a
// set+number miss can fall back to a name lookup without re-parsing.
type Row struct {
	Ident     scryfall.Identifier
	Name      string
	Finish    string // nonfoil|foil|etched — Scryfall's spelling, hoard's too
	Op        string // under|over
	Threshold float64
}

// Request states this row as the resolve pipeline's input.
func (r Row) Request() resolve.Request {
	return resolve.Request{Ident: r.Ident, Name: r.Name, Finish: r.Finish}
}

// Parse reads one watch-list file, recognizing the format by content: a
// document whose first byte is '[' is the JSON array, everything else is
// CSV. Content beats extension because the file is often generated, and a
// generator's naming habits are not part of the contract.
func Parse(data []byte) ([]Row, error) {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\ufeff")))
	if len(trimmed) == 0 {
		return nil, errors.New("empty file")
	}
	if trimmed[0] == '[' {
		return parseJSON(trimmed)
	}
	return parseCSV(bytes.NewReader(data))
}

// identFor picks the strongest resolution scheme the row offers, mirroring
// collsource and decksource: an ID is exact, set+number names one printing,
// a bare name lets Scryfall pick one.
func identFor(id, set, number, name string) scryfall.Identifier {
	switch {
	case id != "":
		return scryfall.Identifier{ID: id}
	case set != "" && number != "":
		return scryfall.Identifier{Set: strings.ToLower(set), CollectorNumber: number}
	default:
		return scryfall.Identifier{Name: name}
	}
}

// normDirection validates the row's direction. There is no default and no
// inference: a watch fires money decisions, so the file must say which way.
func normDirection(s string) (string, error) {
	op := strings.ToLower(strings.TrimSpace(s))
	if op != "under" && op != "over" {
		return "", fmt.Errorf("direction must be under or over, not %q", s)
	}
	return op, nil
}

// parseThreshold reads a positive dollar amount, tolerating a leading $.
func parseThreshold(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(s), "$"), 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse threshold %q", s)
	}
	if v <= 0 {
		return 0, errors.New("threshold must be a positive dollar amount")
	}
	return v, nil
}

// normFinish maps the file's finish cell to the finish vocabulary
// (nonfoil|foil|etched). Anything unrecognized — including empty — reads as
// nonfoil: the resolver corrects a finish the printing lacks, while an
// invented foil would watch a price that may not exist.
func normFinish(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "foil", "true", "yes", "1":
		return "foil"
	case "etched", "foil-etched", "etched foil":
		return "etched"
	default:
		return "nonfoil"
	}
}
