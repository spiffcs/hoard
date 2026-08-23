package report

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func Binders(env ui.Env, binders []store.DeckSummary) string {
	t := ui.Table{
		Env: env, Header: true,
		Cols: []ui.Col{
			{Title: "ID", Align: ui.Right, Style: env.Dim()},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8},
			{Title: "CARDS", Align: ui.Right},
			{Title: "VALUE", Align: ui.Right},
		},
	}
	var uncounted bool
	for _, b := range binders {
		name := b.Name
		if !b.Counted {
			name += " *"
			uncounted = true
		}
		t.Add(ui.C(fmt.Sprint(b.ID)), ui.C(name),
			ui.C(ui.Count(b.TotalCopies)), ui.C(ui.Money(b.Value)))
	}
	out := t.Render()
	if uncounted {
		out += env.Dim()("* not counted toward your collection") + "\n"
	}
	return out
}

func FinishRepairs(env ui.Env, fixed []store.FinishFix) string {
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right},
			{Title: "WAS", Align: ui.Left, Style: env.Dim()},
			{Title: "NOW", Align: ui.Left},
			{Title: "IN", Align: ui.Left, Flex: true, Min: 10, Max: 30,
				Priority: 5, Style: env.Dim()},
		},
	}
	for _, f := range fixed {
		t.Add(ui.C(f.Name), ui.C(ui.Printing(f.SetCode, f.CollectorNumber)),
			ui.C(ui.Count(f.Quantity)), ui.C(f.From.String()), ui.C(f.To.String()), ui.C(f.Container))
	}
	return t.Render()
}
