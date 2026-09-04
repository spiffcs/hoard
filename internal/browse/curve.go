package browse

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const (
	curveWidth    = 28
	curveBarCells = 12
	curveTopBuckt = 7
)

func (m Model) curveShown() bool {
	if m.view != viewHoldings || !m.inDeck() {
		return false
	}
	return m.curve.Spells() > 0 || m.curve.Lands > 0 || m.curve.Unknown > 0
}

// cardsWidth is how wide the card table draws. With room for the curve beside
// it the table takes only the columns it needs, so the curve sits against it
// rather than out at the pane's edge; otherwise it takes the whole pane.
func (m Model) cardsWidth() int {
	_, right := m.paneWidths()
	if !m.curveShown() {
		return right
	}
	table := m.cardTableWidth(right)
	if right-table-paneGap >= curveWidth {
		return table
	}
	return right
}

func (m Model) curveBelowRows() int {
	_, right := m.paneWidths()
	if !m.curveShown() || m.cardsWidth() < right {
		return 0
	}
	rows := len(m.curveLines(curveWidth)) + 1
	if m.visibleRows()-rows < minCardListRows {
		return 0
	}
	return rows
}

func curveBuckets(c store.Curve) []store.CurveRow {
	copies := map[int]int{}
	for _, r := range c.Rows {
		copies[min(r.Mana, curveTopBuckt)] += r.Copies
	}
	top := 0
	for mana := range copies {
		top = max(top, mana)
	}
	out := make([]store.CurveRow, 0, top+1)
	for mana := 0; mana <= max(top, 3); mana++ {
		out = append(out, store.CurveRow{Mana: mana, Copies: copies[mana]})
	}
	return out
}

func curveManaLabel(mana int) string {
	if mana >= curveTopBuckt {
		return fmt.Sprintf("%d+", mana)
	}
	return fmt.Sprint(mana)
}

func (m Model) curveLines(width int) []string {
	c := m.curve
	spells := c.Spells()
	head := m.theme.Title.Render("MANA CURVE")
	if spells > 0 {
		head += m.theme.Help.Render(fmt.Sprintf("  %s", ui.PluralCount(spells, "spell", "spells")))
	}
	out := []string{head, ""}

	if spells == 0 {
		return append(out, m.theme.Help.Render("  nothing here has a mana value yet"))
	}

	env := ui.Env{Width: max(width-2, 0), Color: m.env.Color, Clamp: true}
	most := 0
	for _, r := range c.Rows {
		most = max(most, r.Copies)
	}
	t := ui.Table{Env: env, Cols: []ui.Col{
		{Title: "", Align: ui.Left, Style: env.Dim()},
		{Title: "", Align: ui.Left, Min: 4, Max: curveBarCells},
		{Title: "", Align: ui.Right},
	}}
	for _, r := range curveBuckets(c) {
		count := ""
		if r.Copies > 0 {
			count = ui.Count(r.Copies)
		}
		t.Add(ui.C(curveManaLabel(r.Mana)),
			ui.C(ui.Bar(float64(r.Copies)/float64(most), curveBarCells)),
			ui.C(count))
	}
	t.AddSpacer()
	t.AddStyled(env.Dim(), ui.C("lands"), ui.C(""), ui.C(ui.Count(c.Lands)))
	if c.Unknown > 0 {
		t.AddStyled(env.Dim(), ui.C("?"), ui.C(""), ui.C(ui.Count(c.Unknown)))
	}
	for _, line := range t.Lines() {
		out = append(out, "  "+line)
	}
	return out
}
