package browse

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
)

type filter struct {
	raw string

	names    []string
	sets     []string
	finishes []string
	boards   []string
	nums     map[string][]store.NumCond

	traits store.TraitFilter
}

func (f filter) empty() bool { return f.raw == "" }

func (f filter) needsCatalog() bool { return !f.traits.Empty() }

var numericKeys = map[string]bool{"qty": true, "price": true, "value": true, "cmc": true}

var knownKeys = map[string]bool{
	"name": true, "set": true, "finish": true, "board": true,
	"qty": true, "price": true, "value": true, "cmc": true,
	"rarity": true, "type": true, "t": true, "artist": true,
	"layout": true, "setname": true, "color": true, "c": true,
}

const keyHelp = "name set finish board qty price value rarity type artist layout setname color"

func parseFilter(s string) (filter, error) {
	f := filter{raw: strings.TrimSpace(s), nums: map[string][]store.NumCond{}}
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
			return filter{}, fmt.Errorf("unknown key %q · try: %s", key, keyHelp)
		}
		if value == "" {
			return filter{}, fmt.Errorf("%s needs a value, e.g. %s:something", key, key)
		}

		if numericKeys[key] {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return filter{}, fmt.Errorf("%s wants a number, not %q", key, value)
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
			return filter{}, fmt.Errorf("%s cannot be compared with %q", key, op)
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
			return filter{}, fmt.Errorf("unknown key %q · try: %s", key, keyHelp)
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

func (f filter) matches(c card, allowed map[string]bool) bool {
	if allowed != nil && !allowed[c.ScryfallID] {
		return false
	}
	for _, n := range f.names {
		if !containsFold(c.Name, n) {
			return false
		}
	}
	for _, s := range f.sets {
		if !containsFold(c.SetCode, s) {
			return false
		}
	}
	for _, fin := range f.finishes {
		if !strings.EqualFold(c.Finish.String(), fin) {
			return false
		}
	}
	for _, b := range f.boards {
		if !strings.EqualFold(c.Board, b) {
			return false
		}
	}
	for key, conds := range f.nums {
		var have float64
		switch key {
		case "qty":
			have = float64(c.Quantity)
		case "price":

			if c.Price == nil {
				return false
			}
			have = *c.Price
		case "value":
			have = c.Value
		}
		for _, cond := range conds {
			if !compare(have, cond) {
				return false
			}
		}
	}
	return true
}

func moverAsCard(c store.PriceChange) card {
	return card{
		ScryfallID:      c.ScryfallID,
		Name:            c.Name,
		SetCode:         c.SetCode,
		CollectorNumber: c.CollectorNumber,
		Finish:          c.Finish,
		Quantity:        c.Copies,
		Price:           &c.New,
		Value:           float64(c.Copies) * c.New,
		ColorIdentity:   c.ColorIdentity,
		Treatment:       c.Treatment,
	}
}

func (f filter) unsupportedOnMovers() string {
	if len(f.boards) > 0 {
		return "board"
	}
	return ""
}

func watchAsCard(w store.WatchStatus) card {
	return card{
		ScryfallID:      w.ScryfallID,
		Name:            w.Name,
		SetCode:         w.SetCode,
		CollectorNumber: w.CollectorNumber,
		Finish:          w.Finish,
		Price:           w.PriceUSD,
		Treatment:       w.Treatment,
	}
}

func unpricedAsCard(r store.UnpricedRow) card {
	return card{
		ScryfallID:      r.ScryfallID,
		Name:            r.Name,
		SetCode:         r.SetCode,
		CollectorNumber: r.CollectorNumber,
		Finish:          r.Finish,
		Quantity:        r.Copies,
		ColorIdentity:   r.ColorIdentity,
		Treatment:       r.Treatment,
	}
}

func (f filter) unsupportedOnWatches() string {
	switch {
	case len(f.boards) > 0:
		return "board"
	case len(f.nums["qty"]) > 0:
		return "qty"
	case len(f.nums["value"]) > 0:
		return "value"
	}
	return ""
}

func marketAsCard(c store.OwnedFinish) card {
	return card{
		ScryfallID:      c.ScryfallID,
		Name:            c.Name,
		SetCode:         c.SetCode,
		CollectorNumber: c.CollectorNumber,
		Finish:          c.Finish,
		Quantity:        c.Copies,
		Value:           c.Value,
		ColorIdentity:   c.ColorIdentity,
		Treatment:       c.Treatment,
	}
}

func (f filter) unsupportedOnMarket() (key, why string) {
	switch {
	case len(f.nums["price"]) > 0:
		return "price", "a row here carries four prices, not one"
	case len(f.boards) > 0:
		return "board", "a market row sums every board"
	}
	return "", ""
}

func (m Model) filterMatchCount() int {
	switch m.view {
	case viewHoldings:
		return len(m.filteredCards)
	case viewMovers:
		return len(m.filteredMovers)
	case viewWatches:

		return m.watchTotalRows()
	case viewMarket:

		return len(m.marketAllRows) + len(m.marketAllComps)
	}
	return -1
}

func (m Model) filterUnsupported() string {
	switch m.view {
	case viewMovers:
		if k := m.filter.unsupportedOnMovers(); k != "" {
			return k + ": does not apply on movers · a mover row sums every board"
		}
	case viewWatches:
		if k := m.filter.unsupportedOnWatches(); k != "" {
			return k + ": does not apply on the watches screen · a watch is a line on a printing, not on copies"
		}
	case viewMarket:

		if k, why := m.filter.unsupportedOnMarket(); k != "" {
			return k + ": does not apply on market · " + why
		}
	}
	return ""
}

func compare(have float64, c store.NumCond) bool {
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

func trendAsCard(r store.TrendRow) card {
	return card{
		ScryfallID:      r.ScryfallID,
		Name:            r.Name,
		SetCode:         r.SetCode,
		CollectorNumber: r.CollectorNumber,
		Finish:          r.Finish,
		Quantity:        r.Copies,
		Price:           &r.Last,
		Value:           float64(r.Copies) * r.Last,
		ColorIdentity:   r.ColorIdentity,
		Treatment:       r.Treatment,
	}
}
