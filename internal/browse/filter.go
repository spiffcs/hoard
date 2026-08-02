package browse

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
)

// filter is a parsed query over the card pane.
//
// It is split in two because the rows on screen and the printings in the
// catalog know different things. A holding knows its finish, board and how many
// copies there are; a printing knows its rarity, type and mana value. The trait
// half becomes one SQL query against the generated columns, the holding half is
// applied to the rows already loaded, and a query using only holding terms —
// which includes every plain `/` search — touches the database not at all.
type filter struct {
	raw string

	// Holding terms, matched against the rows on screen.
	names    []string
	sets     []string
	finishes []string
	boards   []string
	nums     map[string][]store.NumCond // qty | price | value

	// Trait terms, matched by the store against the catalog.
	traits store.TraitFilter
}

func (f filter) empty() bool { return f.raw == "" }

// needsCatalog reports whether answering this filter requires a query.
func (f filter) needsCatalog() bool { return !f.traits.Empty() }

// numericKeys are the fields that accept comparisons rather than substrings.
var numericKeys = map[string]bool{"qty": true, "price": true, "value": true, "cmc": true}

// knownKeys is every key the switch below accepts, kept beside it so a term
// added to one and not the other fails loudly at the parse rather than silently
// at the match.
var knownKeys = map[string]bool{
	"name": true, "set": true, "finish": true, "board": true,
	"qty": true, "price": true, "value": true, "cmc": true,
	"rarity": true, "type": true, "t": true, "artist": true,
	"layout": true, "setname": true, "color": true, "c": true,
}

// keyHelp lists every key, for the error a mistyped one produces. A filter bar
// that says only "unknown key" leaves you guessing at the vocabulary.
const keyHelp = "name set finish board qty price value rarity type artist layout setname color"

// parseFilter reads a filter expression.
//
// Terms are ANDed; there is deliberately no OR. Every query this is for narrows
// — "the red mythics I own more than two of" — and adding a precedence to get
// wrong buys nothing for a bar you type into one-handed.
//
// A bare word matches the card name, so the common case is just typing.
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
		// The key is checked before the value, so `sol>` reports that "sol" is
		// not a key rather than that it needs a value — which would send you
		// looking for the right value for a term that does not exist.
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
			// color:WU is two colours, not one two-letter one.
			for _, r := range value {
				f.traits.Colors = append(f.traits.Colors, string(r))
			}
		default:
			return filter{}, fmt.Errorf("unknown key %q · try: %s", key, keyHelp)
		}
	}
	return f, nil
}

// tokenize splits on spaces, keeping double-quoted runs together so a value can
// contain one ("legendary creature", "Seb McKinnon").
//
// An unclosed quote takes the rest of the line rather than failing: the bar is
// filtered as you type, and every partly-typed quoted value would otherwise be
// an error message flashing under the cursor.
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

// splitTerm breaks a token into key, operator and value. A token with no
// operator is a bare word: key is empty and the whole token is the value.
func splitTerm(tok string) (key, op, value string) {
	for _, o := range []string{">=", "<=", ":", ">", "<", "="} {
		if i := strings.Index(tok, o); i > 0 {
			return strings.ToLower(tok[:i]), o, tok[i+len(o):]
		}
	}
	return "", "", tok
}

// matches reports whether one row survives the holding half of the filter.
//
// allowed is the id set the trait half produced, or nil when there were no
// trait terms — nil means "no opinion", not "nothing matched", which is why it
// is checked for length rather than for membership directly.
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
		if !strings.EqualFold(c.Finish, fin) {
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
			// A card no source can price is not "priced at zero"; it is
			// unpriced, and must not satisfy price<1.
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
