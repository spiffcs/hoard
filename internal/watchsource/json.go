package watchsource

import (
	"encoding/json"
	"errors"
	"fmt"
)

// entry is one object of the JSON array. Field names reuse the hoardjson
// vocabulary (scryfallId, setCode, thresholdUsd) so hoard's input and output
// contracts speak one dialect. Unknown keys are ignored — the CSV side
// tolerates extra columns, and a generator's private fields are its own.
type entry struct {
	Name         string  `json:"name"`
	Direction    string  `json:"direction"`
	ThresholdUSD float64 `json:"thresholdUsd"`
	Finish       string  `json:"finish"`
	SetCode      string  `json:"setCode"`
	Number       string  `json:"number"`
	ScryfallID   string  `json:"scryfallId"`
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
		if e.ThresholdUSD <= 0 {
			return nil, fmt.Errorf("entry %d (%s): threshold must be a positive dollar amount", i+1, e.Name)
		}

		out = append(out, Row{
			Ident:     identFor(e.ScryfallID, e.SetCode, e.Number, e.Name),
			Name:      e.Name,
			Finish:    normFinish(e.Finish),
			Op:        op,
			Threshold: e.ThresholdUSD,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("no watches found in file")
	}
	return out, nil
}
