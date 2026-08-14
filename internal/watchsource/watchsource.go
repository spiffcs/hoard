package watchsource

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

type Row struct {
	Ident      scryfall.Identifier
	Name       string
	Finish     finish.Finish
	Op         string
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
}

func (r Row) Request() resolve.Request {
	return resolve.Request{Ident: r.Ident, Name: r.Name, Finish: r.Finish}
}

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

func normDirection(s string) (string, error) {
	switch op := strings.ToLower(strings.TrimSpace(s)); op {
	case "under", "over", "drop", "rise":
		return op, nil
	default:
		return "", fmt.Errorf("direction must be under, over, drop or rise, not %q", s)
	}
}

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

func normFinish(s string) finish.Finish {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "foil", "true", "yes", "1":
		return finish.Foil
	case "etched", "foil-etched", "etched foil":
		return finish.Etched
	default:
		return finish.Nonfoil
	}
}
