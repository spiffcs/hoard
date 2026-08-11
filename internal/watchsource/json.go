package watchsource

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// entry is one object of the JSON array. Field names reuse the hoardjson
// vocabulary (scryfallId, setCode, thresholdUsd) so hoard's input and output
// contracts speak one dialect. Unknown keys are ignored — the CSV side
// tolerates extra columns, and a generator's private fields are its own.
type entry struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
	// The two size fields are pointers so an absent one is distinguishable
	// from a zero. A JSON number cannot be blank the way a CSV cell can, and
	// the exclusivity rule needs to know which the file actually stated —
	// without that, every absolute watch would read as also claiming a
	// percentage of zero.
	ThresholdUSD *float64 `json:"thresholdUsd"`
	Percent      *float64 `json:"percent"`
	MinMoveUSD   *float64 `json:"minMoveUsd"`
	SinceDays    int      `json:"sinceDays"`
	Finish       string   `json:"finish"`
	SetCode      string   `json:"setCode"`
	Number       string   `json:"number"`
	ScryfallID   string   `json:"scryfallId"`
}

// num renders an optional JSON number as the text units reads, so both file
// formats meet one rule rather than each carrying its own copy of it.
func num(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// pctText is num for the percent field, which is a fraction here and a
// percentage in the CSV.
//
// The asymmetry is deliberate and is the one place the two representations
// meet. This document's percent means what hoardjson.Watch.Percent means — 0.1
// is ten percent — so a watch list hoard emitted reads back in unchanged. A
// CSV is written by hand, where "10" is what a person types and "0.1" is the
// slip store.ParsePercent exists to catch. Scaling here lets both be true.
func pctText(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p*100, 'f', -1, 64)
}

func days(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n) + "d"
}

func parseJSON(data []byte) ([]Row, error) {
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.New("not a watch list: want a JSON array of {name, direction, thresholdUsd} objects")
	}
	var out []Row
	for i, e := range entries {
		if e.Name == "" {
			return nil, fmt.Errorf("entry %d: no card name", i+1)
		}
		op, err := normDirection(e.Direction)
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s): %v", i+1, e.Name, err)
		}
		row, err := units(op, num(e.ThresholdUSD), pctText(e.Percent), num(e.MinMoveUSD), days(e.SinceDays))
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s): %v", i+1, e.Name, err)
		}
		row.Ident = identFor(e.ScryfallID, e.SetCode, e.Number, e.Name)
		row.Name = e.Name
		row.Finish = normFinish(e.Finish)
		row.Op = op
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, errors.New("no watches found in file")
	}
	return out, nil
}
