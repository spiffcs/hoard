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
	Container   string
	Kind        string
	Board       string
	PriceUSD    *float64
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
			r.ScryfallID, r.Container, r.Kind, r.Board, price(r.PriceUSD),
		})
	}
	cw.Flush()
	return cw.Error()
}

// canonicalHeader is shared with the import sniffer, which recognizes hoard's
// own files by these columns. Container Kind arrived after the first release;
// the importer treats a file without it as all binder rows, which is what
// those older files were.
var canonicalHeader = []string{
	"Count", "Name", "Set", "Collector Number", "Finish",
	"Scryfall ID", "Container", "Container Kind", "Board", "Price USD",
}

// CanonicalHeader returns the canonical CSV's column names, in order.
func CanonicalHeader() []string {
	return append([]string(nil), canonicalHeader...)
}

// WriteMoxfield writes Moxfield's collection-import columns. Condition and
// Language are hardcoded to their commonest values because hoard does not
// track either; Moxfield requires the columns.
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
			strconv.Itoa(r.Count), r.Name, r.Set, "Near Mint", "English",
			foil, r.CollectorNumber,
		})
	}
	cw.Flush()
	return cw.Error()
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
	type key struct{ id, finish string }
	idx := make(map[key]int, len(rows))
	var out []Row
	for _, r := range rows {
		k := key{r.ScryfallID, r.Finish}
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
		default:
			return a.Finish < b.Finish
		}
	})
	return out
}

// price renders a nullable price, keeping "unknown" distinct from "$0.00".
func price(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}
