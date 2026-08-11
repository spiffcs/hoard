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
	"github.com/spiffcs/hoard/internal/store"
)

// Row is one watch to stand.
//
// Ident carries the single best resolution scheme the file offered (Scryfall
// ID, else set+number, else name); Name is kept alongside even then, so a
// set+number miss can fall back to a name lookup without re-parsing.
// Threshold and Pct are the two units a direction can be stated in, and
// exactly one of them is ever set — under and over are dollar lines, drop and
// rise are movements. MinMove and WindowDays are a movement's floor and
// lookback, defaulted where the file is silent.
type Row struct {
	Ident      scryfall.Identifier
	Name       string
	Finish     string // nonfoil|foil|etched — Scryfall's spelling, hoard's too
	Op         string // under|over|drop|rise
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
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
//
// The four words are two pairs in different units — under and over name a
// place, drop and rise name a movement — and which pair the row used decides
// which of its other cells may be filled. See units.
func normDirection(s string) (string, error) {
	switch op := strings.ToLower(strings.TrimSpace(s)); op {
	case "under", "over", "drop", "rise":
		return op, nil
	default:
		return "", fmt.Errorf("direction must be under, over, drop or rise, not %q", s)
	}
}

// units reads whichever of a row's two size cells its direction calls for, and
// refuses a row that fills both.
//
// The mutual exclusion is enforced rather than resolved by precedence, for the
// same reason normDirection infers nothing: a file that states both a dollar
// line and a percentage has not said which one the alert obeys, and silently
// picking one would stand a watch the file never asked for. A row is refused
// by its line number, the way an over-long row already is.
//
// Where the file is silent on a movement's floor or window, the defaults are
// the ones `watch add` applies, so a watch means the same thing however it was
// created.
func units(op, threshold, percent, minMove, since string) (Row, error) {
	var r Row
	hasThreshold, hasPercent := strings.TrimSpace(threshold) != "", strings.TrimSpace(percent) != ""
	if hasThreshold && hasPercent {
		return r, errors.New("a row states a threshold or a percentage, not both")
	}
	switch op {
	case "under", "over":
		if hasPercent {
			return r, fmt.Errorf("%s is a dollar threshold and takes no percentage", op)
		}
		if !hasThreshold {
			return r, errors.New("threshold must be a positive dollar amount")
		}
		v, err := parseThreshold(threshold)
		if err != nil {
			return r, err
		}
		r.Threshold = v
		return r, nil
	default:
		if hasThreshold {
			return r, fmt.Errorf("%s is a movement and takes no dollar threshold", op)
		}
		if !hasPercent {
			return r, fmt.Errorf("%s needs a percentage", op)
		}
		pct, err := store.ParsePercent(op, percent)
		if err != nil {
			return r, fmt.Errorf("percent: %v", err)
		}
		r.Pct = pct
		r.MinMove = store.DefaultMinMove
		if s := strings.TrimSpace(minMove); s != "" {
			v, err := strconv.ParseFloat(strings.TrimPrefix(s, "$"), 64)
			if err != nil || v < 0 {
				return r, fmt.Errorf("cannot parse minimum move %q", minMove)
			}
			r.MinMove = v
		}
		r.WindowDays = store.DefaultWindowDays
		if s := strings.TrimSpace(since); s != "" {
			days, err := parseSince(s)
			if err != nil {
				return r, err
			}
			r.WindowDays = days
		}
		return r, nil
	}
}

// parseSince reads a lookback in the vocabulary `hoard movers --since` uses —
// 30d, 2w — and answers in whole days, which is what a watch stores.
func parseSince(s string) (int, error) {
	n, err := strconv.ParseFloat(strings.TrimRight(s, "dwDW"), 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("cannot parse a window from %q: want something like 30d or 2w", s)
	}
	switch s[len(s)-1] {
	case 'w', 'W':
		n *= 7
	case 'd', 'D':
	default:
		return 0, fmt.Errorf("cannot parse a window from %q: want something like 30d or 2w", s)
	}
	if int(n) < 1 {
		return 0, fmt.Errorf("%q is less than a day; a movement needs a window to move in", s)
	}
	return int(n), nil
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
