package report

// Tables for the commands whose layouts used to live inline in the command
// files. They belong here with the rest so a column spec exists exactly once —
// the browse package renders its own pane-shaped variants on purpose (a
// scrolling half-width pane earns different priorities than a full terminal),
// but a CLI table has no such excuse. Everything here follows the package
// contract: pure functions from domain values to a string.

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// Binders lists every binder with its rolled-up counts and value.
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
	for _, b := range binders {
		t.Add(ui.C(fmt.Sprint(b.ID)), ui.C(b.Name),
			ui.C(ui.Count(b.TotalCopies)), ui.C(ui.Money(b.Value)))
	}
	return t.Render()
}

// FinishRepairs lists the entries repair-finishes corrected.
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
			ui.C(ui.Count(f.Quantity)), ui.C(f.From), ui.C(f.To), ui.C(f.Container))
	}
	return t.Render()
}
