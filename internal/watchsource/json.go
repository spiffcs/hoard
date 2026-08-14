package watchsource

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type entry struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`

	ThresholdUSD *float64 `json:"thresholdUsd"`
	Percent      *float64 `json:"percent"`
	MinMoveUSD   *float64 `json:"minMoveUsd"`
	SinceDays    int      `json:"sinceDays"`
	Finish       string   `json:"finish"`
	SetCode      string   `json:"setCode"`
	Number       string   `json:"number"`
	ScryfallID   string   `json:"scryfallId"`
}

func num(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

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
