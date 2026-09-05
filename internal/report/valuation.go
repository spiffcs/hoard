package report

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type ValuationData struct {
	AsOf    string
	Binder  store.CollectionTotals
	Binders []store.DeckSummary
	Decks   []store.DeckSummary
	Top     []store.OwnedFinish

	Sources  []store.SourceCount
	Unpriced store.SourceCount
}

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
		return strings.Compare(a.Finish.String(), b.Finish.String())
	})
	return out
}

func asOfDate(stamp string) string { return ui.AsOfDate(stamp) }

func Valuation(env ui.Env, d ValuationData) string {
	var b strings.Builder
	title := "VALUATION"
	if d.AsOf != "" {
		title += " · prices as of " + asOfDate(d.AsOf)
	}
	b.WriteString(env.Bold()(title) + "\n\n")
	b.WriteString(Summary(env, d.Binder, d.Decks))

	if d.Binder.Spent > 0 {
		gain := d.Binder.Value - d.Binder.Spent
		sign := 0
		if gain > 0 {
			sign = 1
		} else if gain < 0 {
			sign = -1
		}
		b.WriteString("\n" + env.Bold()("COST BASIS") + "\n")
		t := ui.Table{
			Env:    env,
			Header: true,
			Cols: []ui.Col{
				{Title: "SPENT", Align: ui.Right},
				{Title: "WORTH", Align: ui.Right},
				{Title: "CHANGE", Align: ui.Right},
			},
		}
		t.Add(ui.C(ui.Money(d.Binder.Spent)), ui.C(ui.Money(d.Binder.Value)),
			ui.Cell{Text: ui.SignedMoney(gain), Style: env.Diverge(float64(sign))})
		b.WriteString(t.Render())
	}

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

func unitPrice(o store.OwnedFinish) float64 {
	if o.UnitPrice != nil {
		return *o.UnitPrice
	}
	if o.Copies == 0 {
		return 0
	}
	return o.Value / float64(o.Copies)
}

func ValuationCSV(w io.Writer, asOf string, owned []store.OwnedFinish) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"Name", "Set", "Collector Number", "Finish", "Copies",
		"Unit Price USD", "Value USD", "As Of"})
	date := asOfDate(asOf)
	if asOf == "" {
		date = ""
	}
	for _, o := range SortOwned(owned) {
		each, value := "", ""
		if !o.Unpriced() {
			each = strconv.FormatFloat(unitPrice(o), 'f', 2, 64)
			value = strconv.FormatFloat(o.Value, 'f', 2, 64)
		}
		cw.Write([]string{
			o.Name, o.SetCode, o.CollectorNumber, o.Finish.String(),
			strconv.Itoa(o.Copies), each, value, date,
		})
	}
	cw.Flush()
	return cw.Error()
}
