package collsource

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// spec names one app's CSV columns. Cells are looked up by header name, never
// by position, so a reordered or extended export still parses; only qty and
// cardName are required to exist, the rest degrade (a missing Scryfall ID
// column just means set+number or name resolution).
type spec struct {
	name     string
	sniff    []string // headers that identify this format; all must be present
	qty      string
	cardName string
	set      string
	number   string
	finish   string
	scryfall string
	binder   string
	kind     string // container kind column; only hoard's own format has one
	// Lossy columns hoard cannot store, counted into Collection.Dropped when
	// a cell actually says something (a non-NM condition, a non-English
	// language, a nonzero price).
	condition, language, price string
}

// specs in sniffing order: hoard's own header first (it is exact), Delver Lens
// last because its single distinctive column is the loosest match.
var specs = []spec{
	{
		name:  "hoard",
		sniff: []string{"Scryfall ID", "Container", "Board"},
		qty:   "Count", cardName: "Name", set: "Set", number: "Collector Number",
		finish: "Finish", scryfall: "Scryfall ID", binder: "Container",
		kind: "Container Kind",
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
	// Delver Lens columns vary by version and export settings; this is the
	// common default. Anything else fails the sniff and the error suggests
	// --format, which is the design: guessing at an unknown dialect silently
	// mangles quantities and finishes.
	{
		name:  "delver",
		sniff: []string{"Card number"},
		qty:   "Quantity", cardName: "Name", set: "Set code", number: "Card number",
		finish: "Foil", scryfall: "Scryfall ID",
		condition: "Condition", language: "Language", price: "Price",
	},
}

// Parse reads one collection CSV. format is a parser name to force, or
// "auto"/"" to recognize the file by its header row.
func Parse(r io.Reader, format string) (*Collection, error) {
	cr := csv.NewReader(r)
	// Ragged rows are tolerated here and cells beyond a row's end read as
	// empty: several apps omit trailing empty fields.
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("empty file")
	}

	header := records[0]
	// Excel and some apps prefix UTF-8 exports with a byte-order mark.
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
		line := n + 2 // 1-based, counting the header
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
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
			Quantity: qty,
			Name:     name,
			Finish:   normFinish(get(rec, sp.finish)),
			Binder:   get(rec, sp.binder),
			Kind:     strings.ToLower(get(rec, sp.kind)),
			Ident:    identFor(get(rec, sp.scryfall), get(rec, sp.set), get(rec, sp.number), name),
		})

		if informativeCondition(get(rec, sp.condition)) {
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

// identFor picks the strongest resolution scheme the row offers, mirroring
// how decksource builds identifiers: an ID is exact, set+number names one
// printing, a bare name lets Scryfall pick one.
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

// parseQty accepts "2" and the "2x"/"x2" stylings some apps emit.
func parseQty(s string) (int, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(s), "x"), "x")
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("cannot parse quantity %q", s)
	}
	return n, nil
}

// normFinish maps each app's foil column to the finish vocabulary
// (nonfoil|foil|etched — Scryfall's spelling, hoard's too). Anything
// unrecognized reads as nonfoil — ManaBox's "normal" among it — because the
// finish repair command exists, while an invented foil would claim a price
// that may not exist.
func normFinish(s string) string {
	switch strings.ToLower(s) {
	case "foil", "true", "yes", "1":
		return "foil"
	case "etched", "foil-etched", "etched foil":
		return "etched"
	default:
		return "nonfoil"
	}
}

// normCondition maps each app's condition column onto the conditions hoard stores
// (unknown|nm|lp|mp|hp|dmg — MTGJSON's, which are TCGplayer's; see
// store.validCondition, which is the gate this must satisfy).
//
// Two scales arrive here and they are not the same scale.
//
//   - TCGplayer's five, which Moxfield and most Delver exports speak:
//     Near Mint, Lightly Played, Moderately Played, Heavily Played, Damaged.
//   - Cardmarket's seven, which ManaBox speaks in words: mint, near_mint,
//     excellent, good, light_played, played, poor.
//
// They barely collide, because each scale's middle values are words the other
// does not use — "excellent" and "good" are only ever Cardmarket, "moderately
// played" only ever TCGplayer. The one genuine ambiguity is Cardmarket's "light
// played" against TCGplayer's "lightly played": nearly the same string, a step
// apart in severity. Both fold to lp, the commoner reading.
//
// Folding seven onto five loses precision, and that is acceptable here for a
// reason worth stating: condition does not affect value in hoard, because no
// source it reads publishes a per-condition price. A condition that lands one
// step generous mislabels a card; it cannot misprice one. The import reports the
// rows it folded rather than folding them silently.
//
// Anything unrecognized reads as unknown rather than as a guess — the same
// instinct as absent-means-unknown everywhere else, and the opposite of
// normFinish's nonfoil default, which can afford a guess because repair-finishes
// exists to undo it.
func normCondition(s string) string {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ") {
	case "":
		return "unknown"

	// Both scales' top, plus Cardmarket's Mint — hoard has nothing above near
	// mint because neither MTGJSON nor TCGplayer does.
	case "near mint", "nm", "nm-mint", "mint", "mt", "m":
		return "nm"

	// TCGplayer's Lightly Played, and Cardmarket's Excellent and Good, which
	// sit between it and Near Mint. Cardmarket's own "light played" lands here
	// too — see the note above.
	//
	// Moxfield spells this one three ways and abbreviates it as SP, not LP:
	// its own documentation gives "Good (Lightly Played)" long and "SP" short,
	// with LP as an alias. Cardsphere calls it "Slightly Played". All of them
	// were falling through to unknown, which reads a stated condition as
	// "nobody said" — the one thing this function exists to prevent.
	case "lightly played", "light played", "lp",
		"good (lightly played)", "good/lightly played", "good lightly played",
		"slightly played", "sp",
		"excellent", "ex", "good", "gd", "g":
		return "lp"

	// TCGplayer's Moderately Played, and Cardmarket's bare Played.
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

// informativeCondition reports whether a cell said something hoard's five
// five values cannot carry exactly — a condition other than near mint, or a word that
// normCondition could not place at all. Those are counted and reported, so a
// seven-value export folded onto five says so instead of arriving quietly.
func informativeCondition(c string) bool {
	switch normCondition(c) {
	case "unknown":
		// Only a cell that said *something* is worth reporting: a blank one is
		// the ordinary case, not a loss.
		return strings.TrimSpace(c) != ""
	case "nm":
		return false
	}
	return true
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
