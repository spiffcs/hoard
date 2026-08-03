package browse

// The watches view: every standing price threshold beside what the card
// costs now, with the rows that met their line leading. It is a plain
// instant database read like movers and unpriced, so it lives in the 'v'
// cycle with them; arbitrage keeps the last slot because the network view
// belongs at the end of the lap.

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// watchesLines renders the watches table.
func (m Model) watchesLines(width int) []string {
	return m.paneLines(paneCards, width, func(env ui.Env) ui.Table {
		t := ui.Table{Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 10},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "WATCH", Align: ui.Left},
			{Title: "PRICE", Align: ui.Right},
			{Title: "STATE", Align: ui.Left, Priority: 3},
		}}
		for _, w := range m.watches {
			price, state, style := "", "unpriced", env.Dim()
			if w.PriceUSD != nil {
				price = ui.Money(*w.PriceUSD)
				if w.Met() {
					// Met rows are the reason the view exists; they carry the
					// only emphasis.
					state, style = "met", env.Bold()
				} else {
					state, style = "waiting", nil
				}
			}
			row := []ui.Cell{
				ui.C(w.Name), ui.C(ui.Printing(w.SetCode, w.CollectorNumber)),
				ui.C(ui.FinishTreated(w.Finish, w.Treatment)),
				ui.C(fmt.Sprintf("%s %s", w.Op, ui.Money(w.Threshold))),
				ui.C(price), ui.C(state),
			}
			if style != nil {
				t.AddStyled(style, row...)
			} else {
				t.Add(row...)
			}
		}
		return t
	})
}

// selectedWatch is the watch under the cards cursor, when the watches view
// is showing.
func (m Model) selectedWatch() *store.WatchStatus {
	if m.view != viewWatches || len(m.watches) == 0 {
		return nil
	}
	i := m.cursor[paneCards]
	if i < 0 || i >= len(m.watches) {
		return nil
	}
	return &m.watches[i]
}
