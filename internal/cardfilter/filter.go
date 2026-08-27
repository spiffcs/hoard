package cardfilter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

type Filter struct {
	raw string

	names    []string
	sets     []string
	finishes []string
	boards   []string
	nums     map[string][]store.NumCond

	traits store.TraitFilter
}

type Subject struct {
	ScryfallID string
	Name       string
	SetCode    string
	Finish     finish.Finish
	Board      string
	Quantity   int
	Price      *float64
	Value      float64
	Paid       *float64
}

func (f Filter) Raw() string { return f.raw }

func (f Filter) Empty() bool { return f.raw == "" }

func (f Filter) NeedsCatalog() bool { return !f.traits.Empty() }

func (f Filter) Traits() store.TraitFilter { return f.traits }

func (f Filter) Uses(key string) bool {
	switch key {
	case "name":
		return len(f.names) > 0
	case "set":
		return len(f.sets) > 0
	case "finish":
		return len(f.finishes) > 0
	case "board":
		return len(f.boards) > 0
	case "qty", "price", "value", "paid":
		return len(f.nums[key]) > 0
	case "cmc":
		return len(f.traits.CMC) > 0
	case "rarity":
		return len(f.traits.Rarities) > 0
	case "type", "t":
		return len(f.traits.Types) > 0
	case "artist":
		return len(f.traits.Artists) > 0
	case "layout":
		return len(f.traits.Layouts) > 0
	case "setname":
		return len(f.traits.SetNames) > 0
	case "color", "c":
		return len(f.traits.Colors) > 0
	}
	return false
}

var numericKeys = map[string]bool{"qty": true, "price": true, "value": true, "cmc": true, "paid": true}

var knownKeys = map[string]bool{
	"name": true, "set": true, "finish": true, "board": true,
	"qty": true, "price": true, "value": true, "cmc": true, "paid": true,
	"rarity": true, "type": true, "t": true, "artist": true,
	"layout": true, "setname": true, "color": true, "c": true,
}

const KeyHelp = "name set finish board qty price value paid rarity type artist layout setname color"

func Parse(s string) (Filter, error) {
	f := Filter{raw: strings.TrimSpace(s), nums: map[string][]store.NumCond{}}
	if f.raw == "" {
		return f, nil
	}

	for _, tok := range tokenize(f.raw) {
		key, op, value := splitTerm(tok)
		if key == "" {
			f.names = append(f.names, value)
			continue
		}

		if !knownKeys[key] {
			return Filter{}, fmt.Errorf("unknown key %q · try: %s", key, KeyHelp)
		}
		if value == "" {
			return Filter{}, fmt.Errorf("%s needs a value, e.g. %s:something", key, key)
		}

		if numericKeys[key] {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return Filter{}, fmt.Errorf("%s wants a number, not %q", key, value)
			}
			cond := store.NumCond{Op: op, Value: n}
			if key == "cmc" {
				f.traits.CMC = append(f.traits.CMC, cond)
			} else {
				f.nums[key] = append(f.nums[key], cond)
			}
			continue
		}
		if op != ":" && op != "=" {
			return Filter{}, fmt.Errorf("%s cannot be compared with %q", key, op)
		}

		switch key {
		case "name":
			f.names = append(f.names, value)
		case "set":
			f.sets = append(f.sets, value)
		case "finish":
			f.finishes = append(f.finishes, value)
		case "board":
			f.boards = append(f.boards, value)
		case "rarity":
			f.traits.Rarities = append(f.traits.Rarities, value)
		case "type", "t":
			f.traits.Types = append(f.traits.Types, value)
		case "artist":
			f.traits.Artists = append(f.traits.Artists, value)
		case "layout":
			f.traits.Layouts = append(f.traits.Layouts, value)
		case "setname":
			f.traits.SetNames = append(f.traits.SetNames, value)
		case "color", "c":

			for _, r := range value {
				f.traits.Colors = append(f.traits.Colors, string(r))
			}
		default:
			return Filter{}, fmt.Errorf("unknown key %q · try: %s", key, KeyHelp)
		}
	}
	return f, nil
}

func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func splitTerm(tok string) (key, op, value string) {
	for _, o := range []string{">=", "<=", ":", ">", "<", "="} {
		if i := strings.Index(tok, o); i > 0 {
			return strings.ToLower(tok[:i]), o, tok[i+len(o):]
		}
	}
	return "", "", tok
}

func (f Filter) Matches(s Subject, allowed map[string]bool) bool {
	if allowed != nil && !allowed[s.ScryfallID] {
		return false
	}
	for _, n := range f.names {
		if !containsFold(s.Name, n) {
			return false
		}
	}
	for _, set := range f.sets {
		if !containsFold(s.SetCode, set) {
			return false
		}
	}
	for _, fin := range f.finishes {
		if !strings.EqualFold(s.Finish.String(), fin) {
			return false
		}
	}
	for _, b := range f.boards {
		if !strings.EqualFold(s.Board, b) {
			return false
		}
	}
	for key, conds := range f.nums {
		var have float64
		switch key {
		case "qty":
			have = float64(s.Quantity)
		case "price":

			if s.Price == nil {
				return false
			}
			have = *s.Price
		case "value":
			have = s.Value
		case "paid":

			if s.Paid == nil {
				return false
			}
			have = *s.Paid
		}
		for _, cond := range conds {
			if !Compare(have, cond) {
				return false
			}
		}
	}
	return true
}

func Compare(have float64, c store.NumCond) bool {
	switch c.Op {
	case ">":
		return have > c.Value
	case ">=":
		return have >= c.Value
	case "<":
		return have < c.Value
	case "<=":
		return have <= c.Value
	default:
		return have == c.Value
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
