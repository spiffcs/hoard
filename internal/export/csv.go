// Package export writes holdings as CSV in the shapes other tools import:
// hoard's own canonical layout (which `hoard import` reads back), Moxfield's
// collection-import columns, and Archidekt's.
//
// Rows are sorted before writing so the same collection always produces the
// same bytes — an export can live in git and its diffs mean something.
package export

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Row is one exported holding: a printing in one finish, in one container.
// Finish uses Scryfall's spelling (nonfoil|foil|etched); each writer maps it to
// its format's vocabulary. A nil PriceUSD means unpriced, which is not the
// same as free — writers emit an empty cell, never 0.00.
//
// Kind says what the container is (binder|deck). The canonical CSV carries it
// so an import can tell loose cards from deck contents — without it, importing
// an --all export would pour every deck into a binder and inflate the totals.
type Row struct {
	Count           int
	Name            string
	Set             string
	CollectorNumber string
	Finish          string
	ScryfallID      string
	// MTGJSONUUID rides along for the JSON emission's card references; no CSV
	// writer carries it (the canonical column set is a compatibility promise
	// shared with the import sniffer).
	MTGJSONUUID string
	// ColorIdentity likewise rides along for the JSON emission only — nil
	// when unknown, empty for colorless (store.Card's semantics).
	ColorIdentity []string
	// Condition is the card's wear — unknown|nm|lp|mp|hp|dmg. The canonical
	// CSV carries it; the foreign shapes have their own vocabulary and map it
	// on the way out.
	Condition string
	// Lang is the printing's language code ("en", "ja"), empty when hoard has
	// not stored the card's document. Carried for the JSON emission and for
	// the Moxfield writer, which requires a Language column and had no choice
	// but to claim English for every row.
	Lang      string
	Container string
	Kind      string
	Board     string
	PriceUSD  *float64
}

// WriteCanonical writes hoard's own CSV: lossless (Scryfall ID makes re-import
// exact, Container/Board keep the organization) yet spreadsheet-friendly via
// Set and Collector Number.
func WriteCanonical(w io.Writer, rows []Row) error {
	rows = Sorted(rows)
	cw := csv.NewWriter(w)
	cw.Write(canonicalHeader)
	for _, r := range rows {
		cw.Write([]string{
			strconv.Itoa(r.Count), r.Name, r.Set, r.CollectorNumber, r.Finish,
			condition(r.Condition),
			r.ScryfallID, r.Container, r.Kind, r.Board, price(r.PriceUSD),
		})
	}
	cw.Flush()
	return cw.Error()
}

// canonicalHeader is shared with the import sniffer, which recognizes hoard's
// own files by these columns. Container Kind arrived after the first release;
// the importer treats a file without it as all binder rows, which is what
// those older files were. Condition arrived with schema v23 on the same terms:
// the sniff does not key on it, cells are read by name, and a file without it
// imports as unassessed — which is what those rows were.
var canonicalHeader = []string{
	"Count", "Name", "Set", "Collector Number", "Finish", "Condition",
	"Scryfall ID", "Container", "Container Kind", "Board", "Price USD",
}

// CanonicalHeader returns the canonical CSV's column names, in order.
func CanonicalHeader() []string {
	return append([]string(nil), canonicalHeader...)
}

// WriteMoxfield writes Moxfield's collection-import columns, in Moxfield's own
// vocabulary for both Condition and Language. Unassessed rows send Near Mint —
// Moxfield requires a value, and that is what this column claimed for every row
// before hoard stored one.
func WriteMoxfield(w io.Writer, rows []Row) error {
	rows = Sorted(aggregated(rows))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Count", "Name", "Edition", "Condition", "Language", "Foil", "Collector Number"})
	for _, r := range rows {
		// Moxfield's Foil column: empty for non-foil, else the finish name.
		foil := r.Finish
		if foil == "nonfoil" {
			foil = ""
		}
		cw.Write([]string{
			strconv.Itoa(r.Count), r.Name, r.Set,
			moxfieldCondition(r.Condition), moxfieldLanguage(r.Lang),
			foil, r.CollectorNumber,
		})
	}
	cw.Flush()
	return cw.Error()
}

// moxfieldCondition renders a condition as the words Moxfield spells them with.
//
// Their scale is TCGplayer's, named differently: what TCGplayer calls Lightly
// Played, Moxfield writes "Good (Lightly Played)" and abbreviates SP. Sending
// their words back means the file round-trips — hoard's own normCondition reads
// every one of these.
//
// Unassessed sends Near Mint because the column is required and a blank is
// rejected. That is a claim hoard cannot support, and it is the same claim this
// writer made for every row before conditions were stored.
func moxfieldCondition(c string) string {
	switch c {
	case "lp":
		return "Good (Lightly Played)"
	case "mp":
		return "Played"
	case "hp":
		return "Heavily Played"
	case "dmg":
		return "Damaged"
	default: // nm, unknown, and anything unexpected
		return "Near Mint"
	}
}

// moxfieldLanguage renders a Scryfall language code as the word Moxfield's
// importer expects.
//
// Unknown means English, which is what this column always said before the
// language was stored: almost every printing is English, and Moxfield rejects
// an empty cell. A code hoard does not have a word for goes through as-is
// rather than being silently relabelled English — a wrong word is easier to
// notice and fix than a wrong assumption.
func moxfieldLanguage(lang string) string {
	switch strings.ToLower(lang) {
	case "", "en":
		return "English"
	case "es":
		return "Spanish"
	case "fr":
		return "French"
	case "de":
		return "German"
	case "it":
		return "Italian"
	case "pt":
		return "Portuguese"
	case "ja":
		return "Japanese"
	case "ko":
		return "Korean"
	case "ru":
		return "Russian"
	case "zhs":
		return "Chinese Simplified"
	case "zht":
		return "Chinese Traditional"
	case "he", "la", "grc", "ar", "sa", "ph":
		// The novelty languages Scryfall carries for a handful of promos.
		// Moxfield has no word for them; sending the code is honest.
		return lang
	default:
		return lang
	}
}

// WriteArchidekt writes Archidekt's collection-import columns; their importer
// tolerates missing optionals, so this is the minimal exact set.
func WriteArchidekt(w io.Writer, rows []Row) error {
	rows = Sorted(aggregated(rows))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Quantity", "Name", "Finish", "Edition Code", "Collector Number", "Scryfall ID"})
	for _, r := range rows {
		finish := map[string]string{"nonfoil": "Normal", "foil": "Foil", "etched": "Etched"}[r.Finish]
		if finish == "" {
			finish = r.Finish
		}
		cw.Write([]string{
			strconv.Itoa(r.Count), r.Name, finish, r.Set, r.CollectorNumber, r.ScryfallID,
		})
	}
	cw.Flush()
	return cw.Error()
}

// aggregated merges rows that differ only by container or board, summing
// counts. The Moxfield and Archidekt shapes have no container column, so
// without this a card held in two binders would export as duplicate lines.
func aggregated(rows []Row) []Row {
	// Condition is part of the key: two rows of one printing that differ only
	// by wear are different things to sell, and merging them would export a
	// near-mint count that includes played copies.
	type key struct{ id, finish, condition string }
	idx := make(map[key]int, len(rows))
	var out []Row
	for _, r := range rows {
		k := key{r.ScryfallID, r.Finish, r.Condition}
		if i, ok := idx[k]; ok {
			out[i].Count += r.Count
			continue
		}
		r.Container, r.Kind, r.Board = "", "", ""
		idx[k] = len(out)
		out = append(out, r)
	}
	return out
}

// Sorted returns the rows in output order without disturbing the caller's
// slice. Container first so a canonical export reads binder by binder; the
// rest pins ties so equal-value rows (which the store orders arbitrarily)
// cannot reshuffle between exports. Exported because the JSON emission
// promises the same canonical order as the CSV writers.
func Sorted(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Container != b.Container:
			return a.Container < b.Container
		case a.Name != b.Name:
			return a.Name < b.Name
		case a.Set != b.Set:
			return a.Set < b.Set
		case a.CollectorNumber != b.CollectorNumber:
			return a.CollectorNumber < b.CollectorNumber
		case a.Finish != b.Finish:
			return a.Finish < b.Finish
		default:
			// The last tiebreak, so two rows differing only by condition keep
			// a stable order and an export stays diffable.
			return a.Condition < b.Condition
		}
	})
	return out
}

// condition renders a holding's condition for the canonical CSV. Unassessed
// writes an empty cell rather than the word: a spreadsheet reads a blank as
// "nothing said", which is the meaning, and an importer that predates the
// column sees exactly what it saw before.
func condition(c string) string {
	if c == "" || c == "unknown" {
		return ""
	}
	return c
}

// price renders a nullable price, keeping "unknown" distinct from "$0.00".
func price(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}
