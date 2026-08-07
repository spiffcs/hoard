package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Align is a column's horizontal alignment.
type Align int

const (
	Left Align = iota
	Right
)

// DefaultGutter matches the padding the CLI's tabwriter tables used.
const DefaultGutter = 2

// Col describes one column's layout. Widths are measured in display cells.
type Col struct {
	Title    string
	Align    Align
	Flex     bool  // absorbs slack, and shrinks first when space runs out
	Min, Max int   // 0 means unbounded
	Priority int   // >0 is droppable; the highest priority is dropped first
	Style    Style // default style for this column's cells
	// Width pins the column's laid-out width regardless of this table's
	// cells: a paged table sizes its columns from the whole ranking so a
	// page turn holds the shape instead of re-fitting to each page's
	// longest value. Space pressure still shrinks and drops columns via
	// Min and Priority; wider cells truncate.
	Width int
}

// Cell is one rendered value.
//
// Text is always unstyled: the renderer measures it to lay out columns, and
// styling is applied afterwards, outside the padding. That invariant is what
// keeps ANSI escapes from corrupting alignment — the failure mode that makes
// text/tabwriter unusable with styled output.
type Cell struct {
	Text  string
	Style Style // nil inherits the column's style
}

// C is shorthand for an unstyled cell.
func C(text string) Cell { return Cell{Text: text} }

// Row is a line of cells, an optional whole-row style override, or a blank
// spacer line.
type Row struct {
	Cells  []Cell
	Style  Style // overrides the column styles for this row
	Spacer bool
}

// Table lays a grid out to fit Env.Width exactly.
type Table struct {
	Env    Env
	Cols   []Col
	Rows   []Row
	Gutter int  // 0 uses DefaultGutter
	Header bool // print the column-title row
}

// Add appends a row of cells.
func (t *Table) Add(cells ...Cell) {
	t.Rows = append(t.Rows, Row{Cells: cells})
}

// AddStyled appends a row rendered entirely in style.
func (t *Table) AddStyled(style Style, cells ...Cell) {
	t.Rows = append(t.Rows, Row{Cells: cells, Style: style})
}

// AddSpacer appends a blank line.
func (t *Table) AddSpacer() {
	t.Rows = append(t.Rows, Row{Spacer: true})
}

func (t Table) gutter() int {
	if t.Gutter <= 0 {
		return DefaultGutter
	}
	return t.Gutter
}

// blankColumns marks the columns whose every cell says nothing, so the fit can
// leave them out.
//
// A column of dashes is width spent on the absence of information. The FINISH
// column against a binder with no foils, or COND before anything has been
// assessed, is a header and a rule of hyphens telling the reader what they
// already know — and it is taken from the card names, which is the column
// people are actually reading.
//
// Only droppable columns qualify. Priority > 0 is the table's existing way of
// saying "this one is optional under pressure", so the same declaration serves
// here: NAME, VALUE and anything else a table insists on are never dropped,
// however empty they look. That is deliberate — a VALUE column of dashes still
// says "these are worth nothing known", which is a fact about the hoard rather
// than about the column.
//
// The mask is computed before the fit and seeds it, so the width a blank column
// would have taken goes to the card names rather than being left as a gap.
//
// Scope is the whole table, not the visible page: an interactive list builds one
// Table from every row and slices the rendered lines afterwards, so a column
// cannot appear and vanish as the cursor moves.
func (t Table) blankColumns() []bool {
	drop := make([]bool, len(t.Cols))
	for i, c := range t.Cols {
		if c.Priority == 0 {
			continue
		}
		blank := true
		for _, r := range t.Rows {
			if r.Spacer || i >= len(r.Cells) {
				continue
			}
			if !saysNothing(r.Cells[i].Text) {
				blank = false
				break
			}
		}
		drop[i] = blank
	}
	return drop
}

// saysNothing reports whether a cell carries information worth a column.
//
// Both marks count, for different reasons — see the constants in format.go.
// A column of em dashes is entirely unknown; a column of hyphens is entirely
// the dull default. Neither tells a reader anything they could act on, which is
// the test here. The distinction between the two still matters everywhere else,
// and is why they are separate characters rather than one.
func saysNothing(text string) bool {
	switch strings.TrimSpace(text) {
	case "", suppressed, unknown:
		return true
	}
	return false
}

// natural returns each column's unconstrained width: the widest of its title
// and its cells, capped at Col.Max.
func (t Table) natural() []int {
	w := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		if t.Header {
			w[i] = ansi.StringWidth(c.Title)
		}
	}
	for _, r := range t.Rows {
		if r.Spacer {
			continue
		}
		for i, cell := range r.Cells {
			if i >= len(w) {
				break
			}
			if n := ansi.StringWidth(cell.Text); n > w[i] {
				w[i] = n
			}
		}
	}
	for i, c := range t.Cols {
		if c.Width > 0 {
			// The pin replaces the cells' measure but never squeezes the
			// column's own title — which is constant, so still page-stable.
			w[i] = c.Width
			if t.Header {
				w[i] = max(w[i], ansi.StringWidth(c.Title))
			}
		}
		if c.Max > 0 && w[i] > c.Max {
			w[i] = c.Max
		}
	}
	return w
}

// fitColumns resolves final column widths for the given natural widths.
//
// It shrinks Flex columns toward their Min first, then shrinks bounded columns
// (a bar, say, declared Min 6 / Max 10) toward their Min, then drops columns by
// descending Priority. If the table still doesn't fit it gives up clamping and
// returns natural widths rather than rendering unreadably narrow cells.
//
// keep[i] reports whether column i is rendered at all.
//
// drop marks columns already ruled out before any width is considered — the
// blank ones. They are excluded from the fit rather than removed after it, so
// the space they would have taken goes to the flexible column instead of being
// left as a gap. Dropping afterwards truncated card names to make room for a
// column of dashes that was then thrown away.
func fitColumns(cols []Col, natural []int, gutter int, env Env, drop []bool) (widths []int, keep []bool) {
	widths = append([]int(nil), natural...)
	keep = make([]bool, len(cols))
	for i := range keep {
		keep[i] = i >= len(drop) || !drop[i]
	}

	// Unlimited width, or a non-terminal: render everything at natural width.
	if !env.Clamp || env.Width <= 0 {
		return widths, keep
	}

	total := func() int {
		sum, n := 0, 0
		for i := range cols {
			if !keep[i] {
				continue
			}
			sum += widths[i]
			n++
		}
		if n > 1 {
			sum += (n - 1) * gutter
		}
		return sum
	}

	if total() <= env.Width {
		return widths, keep
	}

	// 1. Shrink flexible columns toward their minimum.
	for i, c := range cols {
		if !c.Flex {
			continue
		}
		over := total() - env.Width
		if over <= 0 {
			break
		}
		floor := max(c.Min, 1)
		if room := widths[i] - floor; room > 0 {
			widths[i] -= min(room, over)
		}
	}
	if total() <= env.Width {
		return widths, keep
	}

	// 2. Shrink bounded columns toward their minimum.
	for i, c := range cols {
		if c.Flex || c.Min <= 0 {
			continue
		}
		over := total() - env.Width
		if over <= 0 {
			break
		}
		if room := widths[i] - c.Min; room > 0 {
			widths[i] -= min(room, over)
		}
	}
	if total() <= env.Width {
		return widths, keep
	}

	// 3. Drop droppable columns, highest priority first.
	for {
		victim, best := -1, 0
		for i, c := range cols {
			if keep[i] && c.Priority > best {
				victim, best = i, c.Priority
			}
		}
		if victim < 0 {
			break
		}
		keep[victim] = false

		// Dropping a column frees space the flexible column can reclaim.
		for i, c := range cols {
			if !keep[i] || !c.Flex {
				continue
			}
			if slack := env.Width - total(); slack > 0 {
				if grow := min(slack, natural[i]-widths[i]); grow > 0 {
					widths[i] += grow
				}
			}
		}
		if total() <= env.Width {
			return widths, keep
		}
	}

	// 4. Still over: give up clamping rather than render 3-character cells.
	if total() > env.Width {
		copy(widths, natural)
		for i := range keep {
			keep[i] = true
		}
	}
	return widths, keep
}

// Render lays the table out and returns it as lines, newline-terminated.
func (t Table) Render() string {
	lines := t.Lines()
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Lines lays the table out and returns each rendered line separately, unterminated.
//
// For callers that must address a line after layout — an interactive list drawing
// a cursor on the selected row.
//
// Order: column titles when Header is set, then one line per row, a spacer
// rendering empty. So with a header and no spacers, row i is line i+1.
func (t Table) Lines() []string {
	if len(t.Cols) == 0 {
		return nil
	}
	widths, keep := fitColumns(t.Cols, t.natural(), t.gutter(), t.Env, t.blankColumns())

	out := make([]string, 0, len(t.Rows)+1)
	if t.Header {
		titles := make([]Cell, len(t.Cols))
		for i, c := range t.Cols {
			titles[i] = Cell{Text: c.Title}
		}
		out = append(out, t.line(Row{Cells: titles, Style: t.Env.Dim()}, widths, keep))
	}
	for _, r := range t.Rows {
		out = append(out, t.line(r, widths, keep))
	}
	return out
}

// line renders one row without its terminating newline, padding outside the
// styled span so that ANSI escapes can never influence column alignment.
func (t Table) line(r Row, widths []int, keep []bool) string {
	if r.Spacer {
		return ""
	}

	gutter := strings.Repeat(" ", t.gutter())
	var b strings.Builder
	first := true

	for i, col := range t.Cols {
		if !keep[i] {
			continue
		}
		if !first {
			b.WriteString(gutter)
		}
		first = false

		var cell Cell
		if i < len(r.Cells) {
			cell = r.Cells[i]
		}

		text := cell.Text
		if w := widths[i]; t.Env.Clamp && ansi.StringWidth(text) > w {
			text = Truncate(text, w)
		}

		style := cell.Style
		if style == nil {
			style = col.Style
		}
		if r.Style != nil {
			style = r.Style
		}
		if style == nil {
			style = plain
		}

		pad := max(widths[i]-ansi.StringWidth(text), 0)
		if col.Align == Right {
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(style(text))
		} else {
			b.WriteString(style(text))
			b.WriteString(strings.Repeat(" ", pad))
		}
	}

	// Trailing padding helps nobody and dirties grep/diff output.
	return strings.TrimRight(b.String(), " ")
}

// Truncate shortens s to at most w display cells, marking the cut with an
// ellipsis. It is width-aware, so wide runes and combining marks are counted
// as they render rather than as bytes.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(s, w-1, "") + "…"
}
