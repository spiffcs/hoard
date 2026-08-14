package ui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/sahilm/fuzzy"
)

const PaletteMaxRows = 8

const PaletteHelp = "enter run · esc close · ↑/↓ choose · type to narrow"

type PaletteItem struct {
	Title string

	Aliases string

	Desc string

	Key string

	Rank int
}

type PaletteMatch struct {
	Index     int
	Positions []int
}

type Palette struct {
	Query   string
	Cursor  int
	matches []PaletteMatch
}

func SpacedTitle(title string) string {
	var b strings.Builder
	runes := []rune(title)
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			boundary := (unicode.IsUpper(r) && !unicode.IsUpper(prev)) ||
				(unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1])) ||
				(unicode.IsDigit(r) && !unicode.IsDigit(prev))
			if boundary {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func (p *Palette) Refresh(items []PaletteItem) {
	p.matches = p.matches[:0]

	if p.Query == "" {
		for i := range items {
			p.matches = append(p.matches, PaletteMatch{Index: i})
		}

		slices.SortStableFunc(p.matches, func(a, b PaletteMatch) int {
			return items[b.Index].Rank - items[a.Index].Rank
		})
	} else {
		targets := make([]string, len(items))
		for i, it := range items {
			targets[i] = it.Title + " " + SpacedTitle(it.Title) + " " + it.Aliases
		}
		for _, fm := range fuzzy.Find(p.Query, targets) {

			titleLen := len([]rune(items[fm.Index].Title))
			var pos []int
			for _, at := range fm.MatchedIndexes {
				if at < titleLen {
					pos = append(pos, at)
				}
			}
			p.matches = append(p.matches, PaletteMatch{Index: fm.Index, Positions: pos})
		}
	}
	p.Cursor = min(p.Cursor, max(len(p.matches)-1, 0))
}

func (p *Palette) Matches() []PaletteMatch { return p.matches }

func (p *Palette) Selected() (int, bool) {
	if len(p.matches) == 0 || p.Cursor >= len(p.matches) {
		return 0, false
	}
	return p.matches[p.Cursor].Index, true
}

func (p *Palette) Up()   { p.Cursor = max(p.Cursor-1, 0) }
func (p *Palette) Down() { p.Cursor = min(p.Cursor+1, max(len(p.matches)-1, 0)) }

func (p *Palette) Type(s string) { p.Query += s }
func (p *Palette) Clear()        { p.Query = "" }
func (p *Palette) Backspace() {
	if p.Query == "" {
		return
	}
	r := []rune(p.Query)
	p.Query = string(r[:len(r)-1])
}

func (p *Palette) Rows() int {
	return max(min(len(p.matches), PaletteMaxRows), 1)
}

func (p *Palette) Lines(items []PaletteItem, width int, th Theme) []string {
	if len(p.matches) == 0 {
		return []string{th.Help.Render("no matching command")}
	}

	shown := p.Rows()
	start := 0
	if p.Cursor >= shown {
		start = p.Cursor - shown + 1
	}

	lines := make([]string, 0, shown)
	for i := start; i < min(start+shown, len(p.matches)); i++ {
		match := p.matches[i]
		it := items[match.Index]

		marker := "  "
		if i == p.Cursor {
			marker = "▸ "
		}

		plainWidth := 2 + len([]rune(it.Title))
		gap := max(width-plainWidth-len([]rune(it.Key))-1, 1)
		line := marker + BoldRunes(it.Title, match.Positions, th) +
			strings.Repeat(" ", gap) + th.Help.Render(it.Key)
		if i == p.Cursor {

			line = th.Cursor.Render(marker+it.Title) +
				strings.Repeat(" ", gap) + th.Help.Render(it.Key)
		}
		lines = append(lines, Truncate(line, width))
	}
	return lines
}

func (p *Palette) Desc(items []PaletteItem) string {
	i, ok := p.Selected()
	if !ok {
		return ""
	}
	return items[i].Desc
}

func BoldRunes(s string, positions []int, th Theme) string {
	if len(positions) == 0 {
		return s
	}
	set := make(map[int]bool, len(positions))
	for _, p := range positions {
		set[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if set[i] {
			b.WriteString(th.Title.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
