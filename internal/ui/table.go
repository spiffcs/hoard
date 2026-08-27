package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type Align int

const (
	Left Align = iota
	Right
)

const DefaultGutter = 2

type Col struct {
	Title    string
	Align    Align
	Flex     bool
	Min, Max int
	Priority int
	Style    Style

	Width int
}

type Cell struct {
	Text  string
	Style Style
}

func C(text string) Cell { return Cell{Text: text} }

type Row struct {
	Cells  []Cell
	Style  Style
	Spacer bool
}

type Table struct {
	Env    Env
	Cols   []Col
	Rows   []Row
	Gutter int
	Header bool
}

func (t *Table) Add(cells ...Cell) {
	t.Rows = append(t.Rows, Row{Cells: cells})
}

func (t *Table) AddStyled(style Style, cells ...Cell) {
	t.Rows = append(t.Rows, Row{Cells: cells, Style: style})
}

func (t *Table) AddSpacer() {
	t.Rows = append(t.Rows, Row{Spacer: true})
}

func (t Table) gutter() int {
	if t.Gutter <= 0 {
		return DefaultGutter
	}
	return t.Gutter
}

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

func saysNothing(text string) bool {
	switch strings.TrimSpace(text) {
	case "", suppressed, unknown:
		return true
	}
	return false
}

func (t Table) natural() []int {
	w := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		if t.Header {
			w[i] = Width(c.Title)
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
			if n := Width(cell.Text); n > w[i] {
				w[i] = n
			}
		}
	}
	for i, c := range t.Cols {
		if c.Width > 0 {

			w[i] = c.Width
			if t.Header {
				w[i] = max(w[i], Width(c.Title))
			}
		}
		if c.Max > 0 && w[i] > c.Max {
			w[i] = c.Max
		}
	}
	return w
}

func fitColumns(cols []Col, natural []int, gutter int, env Env, drop []bool) (widths []int, keep []bool) {
	widths = append([]int(nil), natural...)
	keep = make([]bool, len(cols))
	for i := range keep {
		keep[i] = i >= len(drop) || !drop[i]
	}

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

	if total() > env.Width {
		copy(widths, natural)
		for i := range keep {
			keep[i] = true
		}
	}
	return widths, keep
}

func (t Table) Render() string {
	lines := t.Lines()
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (t Table) Lines() []string { return t.WindowLines(0, len(t.Rows)) }

func (t Table) WindowLines(start, count int) []string {
	if len(t.Cols) == 0 {
		return nil
	}
	widths, keep := fitColumns(t.Cols, t.natural(), t.gutter(), t.Env, t.blankColumns())

	start = min(max(start, 0), len(t.Rows))
	end := min(start+max(count, 0), len(t.Rows))

	out := make([]string, 0, end-start+1)
	if t.Header {
		titles := make([]Cell, len(t.Cols))
		for i, c := range t.Cols {
			titles[i] = Cell{Text: c.Title}
		}
		out = append(out, t.line(Row{Cells: titles, Style: t.Env.Dim()}, widths, keep))
	}
	for _, r := range t.Rows[start:end] {
		out = append(out, t.line(r, widths, keep))
	}
	return out
}

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
		if w := widths[i]; t.Env.Clamp && Width(text) > w {
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

		pad := max(widths[i]-Width(text), 0)
		if col.Align == Right {
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(style(text))
		} else {
			b.WriteString(style(text))
			b.WriteString(strings.Repeat(" ", pad))
		}
	}

	return strings.TrimRight(b.String(), " ")
}

func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(s, w-1, "") + "…"
}
