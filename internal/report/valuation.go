package report

// The valuation report: what the hoard is worth, itemized and dated, in a
// form worth handing to an insurer or a buyer. Everything here is
// deterministic for the same data — the report is meant to live in a folder
// (or git) and its diffs to mean something.

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// ValuationData is everything the valuation report states. The caller
// assembles it from the store; Top is already sliced to the rows the report
// should itemize (the full list is the CSV's job).
type ValuationData struct {
	// AsOf is when the prices were observed (RFC 3339), empty when nothing
	// has ever been priced.
	AsOf    string
	Binder  store.CollectionTotals
	Binders []store.DeckSummary
	Decks   []store.DeckSummary
	Top     []store.OwnedFinish
	// Sources is where the prices come from; Unpriced is the residue no
	// source covers (its Source field is empty).
	Sources  []store.SourceCount
	Unpriced store.SourceCount
}

// SortOwned pins the store's value-descending order with a full tiebreak, so
// equal-value rows cannot reshuffle between two reports over the same data.
func SortOwned(owned []store.OwnedFinish) []store.OwnedFinish {
	out := append([]store.OwnedFinish(nil), owned...)
	slices.SortStableFunc(out, func(a, b store.OwnedFinish) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		if c := strings.Compare(a.ScryfallID, b.ScryfallID); c != 0 {
			return c
		}
		return strings.Compare(a.Finish, b.Finish)
	})
	return out
}

// asOfDate renders an RFC 3339 stamp as the date a reader wants; a stamp that
// does not parse is shown raw rather than dropped — a wrong-looking date on a
// valuation is better than no date.
func asOfDate(stamp string) string {
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		return t.Local().Format("2 Jan 2006")
	}
	return stamp
}

// Valuation renders the report as text.
func Valuation(env ui.Env, d ValuationData) string {
	var b strings.Builder
	title := "VALUATION"
	if d.AsOf != "" {
		title += " · prices as of " + asOfDate(d.AsOf)
	}
	b.WriteString(env.Bold()(title) + "\n\n")
	b.WriteString(Summary(env, d.Binder, d.Decks))

	// A breakdown of one binder restates the BINDER line above it.
	if len(d.Binders) > 1 {
		b.WriteString("\n" + env.Bold()("BINDERS") + "\n")
		t := ui.Table{
			Env:    env,
			Header: true,
			Cols: []ui.Col{
				{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
				{Title: "CARDS", Align: ui.Right, Priority: 2},
				{Title: "COPIES", Align: ui.Right},
				{Title: "VALUE", Align: ui.Right},
			},
		}
		for _, bd := range d.Binders {
			t.Add(ui.C(bd.Name), ui.C(ui.Count(bd.DistinctCards)),
				ui.C(ui.Count(bd.TotalCopies)), ui.C(ui.Money(bd.Value)))
		}
		b.WriteString(t.Render())
	}

	if len(d.Top) > 0 {
		b.WriteString("\n" + env.Bold()(fmt.Sprintf("TOP %d HOLDINGS", len(d.Top))) + "\n")
		t := ui.Table{
			Env:    env,
			Header: true,
			Cols: []ui.Col{
				{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},
				{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
				{Title: "FINISH", Align: ui.Left, Priority: 2, Style: env.Dim()},
				{Title: "COPIES", Align: ui.Right},
				{Title: "EACH", Align: ui.Right, Priority: 4, Style: env.Dim()},
				{Title: "VALUE", Align: ui.Right},
			},
		}
		for _, o := range d.Top {
			t.Add(ui.C(o.Name), ui.C(ui.Printing(o.SetCode, o.CollectorNumber)),
				ui.C(ui.FinishTreated(o.Finish, o.Treatment)), ui.C(ui.Count(o.Copies)),
				ui.C(ui.Money(unitPrice(o))), ui.C(ui.Money(o.Value)))
		}
		b.WriteString(t.Render())
	}

	b.WriteString("\n" + env.Bold()("PRICE SOURCES") + "\n")
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "SOURCE", Align: ui.Left, Flex: true, Min: 10},
			{Title: "PRINTINGS", Align: ui.Right},
			{Title: "COPIES", Align: ui.Right},
		},
	}
	for _, sc := range d.Sources {
		t.Add(ui.C(sc.Source), ui.C(ui.Count(sc.Printings)), ui.C(ui.Count(sc.Copies)))
	}
	if d.Unpriced.Printings > 0 {
		t.AddStyled(env.Dim(), ui.C("unpriced"),
			ui.C(ui.Count(d.Unpriced.Printings)), ui.C(ui.Count(d.Unpriced.Copies)))
	}
	b.WriteString(t.Render())
	if d.Unpriced.Printings > 0 {
		b.WriteString(env.Dim()("Unpriced copies are counted as $0.00 (see hoard unpriced).") + "\n")
	}
	return b.String()
}

// unitPrice is one copy's price, recovered from the row's total. Value is
// quantity × price by construction, so the division is exact to the cent.
func unitPrice(o store.OwnedFinish) float64 {
	if o.Copies == 0 {
		return 0
	}
	return o.Value / float64(o.Copies)
}

// ValuationCSV writes the full itemized valuation — every owned
// printing-finish with its price, not the display's top-N — dated on every
// row, because a CSV file separated from the command that made it should
// still say when its prices were true.
func ValuationCSV(w io.Writer, asOf string, owned []store.OwnedFinish) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"Name", "Set", "Collector Number", "Finish", "Copies",
		"Unit Price USD", "Value USD", "As Of"})
	date := asOfDate(asOf)
	if asOf == "" {
		date = ""
	}
	for _, o := range SortOwned(owned) {
		cw.Write([]string{
			o.Name, o.SetCode, o.CollectorNumber, o.Finish,
			strconv.Itoa(o.Copies),
			strconv.FormatFloat(unitPrice(o), 'f', 2, 64),
			strconv.FormatFloat(o.Value, 'f', 2, 64),
			date,
		})
	}
	cw.Flush()
	return cw.Error()
}
