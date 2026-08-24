package export

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

type Row struct {
	Count           int
	Name            string
	Set             string
	CollectorNumber string
	Finish          finish.Finish
	ScryfallID      string

	MTGJSONUUID string

	ColorIdentity []string

	Detail *store.CardDetail

	Condition string

	Lang      string
	Container string
	Kind      string
	Board     string
	PriceUSD  *float64
}

func WriteCanonical(w io.Writer, rows []Row) error {
	rows = Sorted(rows)
	cw := csv.NewWriter(w)
	cw.Write(canonicalHeader)
	for _, r := range rows {
		cw.Write([]string{
			strconv.Itoa(r.Count), r.Name, r.Set, r.CollectorNumber, r.Finish.String(),
			condition(r.Condition),
			r.ScryfallID, r.Container, r.Kind, r.Board, price(r.PriceUSD),
		})
	}
	cw.Flush()
	return cw.Error()
}

var canonicalHeader = []string{
	"Count", "Name", "Set", "Collector Number", "Finish", "Condition",
	"Scryfall ID", "Container", "Container Kind", "Board", "Price USD",
}

func WriteMoxfield(w io.Writer, rows []Row) error {
	rows = Sorted(aggregated(rows))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Count", "Name", "Edition", "Condition", "Language", "Foil", "Collector Number"})
	for _, r := range rows {

		foil := r.Finish.String()
		if r.Finish == finish.Nonfoil {
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
	default:
		return "Near Mint"
	}
}

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

		return lang
	default:
		return lang
	}
}

func WriteArchidekt(w io.Writer, rows []Row) error {
	rows = Sorted(aggregated(rows))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Quantity", "Name", "Finish", "Edition Code", "Collector Number", "Scryfall ID"})
	for _, r := range rows {
		name := map[finish.Finish]string{
			finish.Nonfoil: "Normal", finish.Foil: "Foil", finish.Etched: "Etched",
		}[r.Finish]
		if name == "" {
			name = r.Finish.String()
		}
		cw.Write([]string{
			strconv.Itoa(r.Count), r.Name, name, r.Set, r.CollectorNumber, r.ScryfallID,
		})
	}
	cw.Flush()
	return cw.Error()
}

func aggregated(rows []Row) []Row {

	type key struct {
		id        string
		finish    finish.Finish
		condition string
	}
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
			return a.Finish.String() < b.Finish.String()
		default:

			return a.Condition < b.Condition
		}
	})
	return out
}

func condition(c string) string {
	if c == "" || c == "unknown" {
		return ""
	}
	return c
}

func price(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}
