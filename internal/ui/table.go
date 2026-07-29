package ui

import (
	"io"
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
		if c.Max > 0 && w[i] > c.Max {
			w[i] = c.Max
		}
	}
	return w
}

// FitColumns resolves final column widths for the given natural widths.
//
// It shrinks Flex columns toward their Min first, then shrinks bounded columns
// (a bar, say, declared Min 6 / Max 10) toward their Min, then drops columns by
// descending Priority. If the table still doesn't fit it gives up clamping and
// returns natural widths rather than rendering unreadably narrow cells.
//
// keep[i] reports whether column i is rendered at all.
func FitColumns(cols []Col, natural []int, gutter int, env Env) (widths []int, keep []bool) {
	widths = append([]int(nil), natural...)
	keep = make([]bool, len(cols))
	for i := range keep {
		keep[i] = true
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

// WriteTo renders the table.
func (t Table) WriteTo(w io.Writer) (int64, error) {
	n, err := io.WriteString(w, t.Render())
	return int64(n), err
}

// Render lays the table out and returns it as lines, newline-terminated.
func (t Table) Render() string {
	if len(t.Cols) == 0 {
		return ""
	}
	widths, keep := FitColumns(t.Cols, t.natural(), t.gutter(), t.Env)

	var b strings.Builder
	if t.Header {
		titles := make([]Cell, len(t.Cols))
		for i, c := range t.Cols {
			titles[i] = Cell{Text: c.Title}
		}
		b.WriteString(t.line(Row{Cells: titles, Style: t.Env.Dim()}, widths, keep))
	}
	for _, r := range t.Rows {
		b.WriteString(t.line(r, widths, keep))
	}
	return b.String()
}

// line renders one row, padding outside the styled span so that ANSI escapes
// can never influence column alignment.
func (t Table) line(r Row, widths []int, keep []bool) string {
	if r.Spacer {
		return "\n"
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
	return strings.TrimRight(b.String(), " ") + "\n"
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
