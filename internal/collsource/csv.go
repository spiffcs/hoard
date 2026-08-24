package collsource

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type spec struct {
	name     string
	sniff    []string
	qty      string
	cardName string
	set      string
	number   string
	finish   string
	scryfall string
	binder   string
	kind     string

	condition, language, price string
}

var specs = []spec{
	{
		name:  "hoard",
		sniff: []string{"Scryfall ID", "Container", "Board"},
		qty:   "Count", cardName: "Name", set: "Set", number: "Collector Number",
		finish: "Finish", scryfall: "Scryfall ID", binder: "Container",
		kind: "Container Kind", condition: "Condition",
	},
	{
		name:  "manabox",
		sniff: []string{"Binder Name", "Scryfall ID"},
		qty:   "Quantity", cardName: "Name", set: "Set code", number: "Collector number",
		finish: "Foil", scryfall: "Scryfall ID", binder: "Binder Name",
		condition: "Condition", language: "Language", price: "Purchase price",
	},
	{
		name:  "moxfield",
		sniff: []string{"Tradelist Count", "Edition"},
		qty:   "Count", cardName: "Name", set: "Edition", number: "Collector Number",
		finish:    "Foil",
		condition: "Condition", language: "Language", price: "Purchase Price",
	},

	{
		name:  "delver",
		sniff: []string{"Card number", "Set code"},
		qty:   "Quantity", cardName: "Name", set: "Set code", number: "Card number",
		finish: "Foil", scryfall: "Scryfall ID",
		condition: "Condition", language: "Language", price: "Price",
	},
}

func Parse(r io.Reader, format string) (*Collection, error) {
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

	sp, err := matchSpec(cols, header, format)
	if err != nil {
		return nil, err
	}

	col := func(name string) int {
		if name == "" {
			return -1
		}
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

	for _, required := range []string{sp.qty, sp.cardName} {
		if col(required) < 0 {
			return nil, fmt.Errorf("%s CSV is missing its %q column (saw: %s)",
				sp.name, required, strings.Join(header, ", "))
		}
	}

	out := &Collection{Format: sp.name, Dropped: map[string]int{}}
	for n, rec := range records[1:] {
		line := n + 2
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}

		if len(rec) > len(header) {
			return nil, fmt.Errorf("line %d: %d fields, header has %d — an unquoted comma in a card name?",
				line, len(rec), len(header))
		}
		name := get(rec, sp.cardName)
		if name == "" {
			return nil, fmt.Errorf("line %d: no card name", line)
		}
		qty, err := parseQty(get(rec, sp.qty))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", line, name, err)
		}

		out.Rows = append(out.Rows, Row{
			Quantity:  qty,
			Name:      name,
			Finish:    normFinish(get(rec, sp.finish)),
			Condition: normCondition(get(rec, sp.condition)),
			Binder:    get(rec, sp.binder),
			Kind:      strings.ToLower(get(rec, sp.kind)),
			Ident:     identFor(get(rec, sp.scryfall), get(rec, sp.set), get(rec, sp.number), name),
		})

		if unplaceableCondition(get(rec, sp.condition)) {
			out.Dropped["condition"]++
		}
		if informativeLanguage(get(rec, sp.language)) {
			out.Dropped["language"]++
		}
		if informativePrice(get(rec, sp.price)) {
			out.Dropped["purchase price"]++
		}
	}
	if len(out.Rows) == 0 {
		return nil, errors.New("no cards found in file")
	}
	return out, nil
}

func matchSpec(cols map[string]int, header []string, format string) (spec, error) {
	if format != "" && format != "auto" {
		for _, sp := range specs {
			if sp.name == format {
				return sp, nil
			}
		}
		return spec{}, fmt.Errorf("unknown format %q (want auto, manabox, moxfield, delver, or hoard)", format)
	}
	for _, sp := range specs {
		matched := true
		for _, h := range sp.sniff {
			if _, ok := cols[strings.ToLower(h)]; !ok {
				matched = false
				break
			}
		}
		if matched {
			return sp, nil
		}
	}
	return spec{}, fmt.Errorf(
		"unrecognized CSV header: %s\nForce a parser with --format manabox|moxfield|delver|hoard",
		strings.Join(header, ", "))
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

func parseQty(s string) (int, error) {
	trimmed := strings.ToLower(s)
	if t := strings.TrimPrefix(trimmed, "x"); t != trimmed {
		trimmed = t
	} else {
		trimmed = strings.TrimSuffix(trimmed, "x")
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("cannot parse quantity %q", s)
	}
	return n, nil
}

func normFinish(s string) finish.Finish {
	switch strings.ToLower(s) {
	case "foil", "true", "yes", "1":
		return finish.Foil
	case "etched", "foil-etched", "etched foil":
		return finish.Etched
	default:
		return finish.Nonfoil
	}
}

func normCondition(s string) string {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ") {
	case "":
		return "unknown"

	case "near mint", "nm", "nm-mint", "mint", "mt", "m":
		return "nm"

	case "lightly played", "light played", "lp",
		"good (lightly played)", "good/lightly played", "good lightly played",
		"slightly played", "sp",
		"excellent", "ex", "good", "gd", "g":
		return "lp"

	case "moderately played", "moderate play", "mp", "played", "pl":
		return "mp"

	case "heavily played", "heavy play", "hp":
		return "hp"

	case "damaged", "dmg", "d", "poor", "po":
		return "dmg"

	default:
		return "unknown"
	}
}

func unplaceableCondition(c string) bool {
	return strings.TrimSpace(c) != "" && normCondition(c) == "unknown"
}

func informativeLanguage(l string) bool {
	switch strings.ToLower(l) {
	case "", "en", "english":
		return false
	}
	return true
}

func informativePrice(p string) bool {
	v, err := strconv.ParseFloat(strings.TrimPrefix(p, "$"), 64)
	return err == nil && v > 0
}
