package ui

// The command palette, shared by every program that has one.
//
// It started in the browser and is here because a second surface wanted the
// same thing. Two fuzzy palettes is how the look drifts apart: one grows a
// description line, the other keeps its key hints in a different column, and
// the two stop being the same control while still being called one.
//
// What lives here is the part that has no opinions — the query, the cursor,
// the fuzzy match and the rows it draws. What does not is the notion of a
// *command*: whether one applies right now, what it runs, and how it is
// ranked all belong to the program whose state answers those questions. So
// callers flatten their own commands into PaletteItem, hand the slice over,
// and map the matched index back through their own registry.
//
// A full-width drawer rather than a floating box: partial-width boxes leave
// table fragments visible around their edges, and a horizontal split has no
// edges to leak around — it is the filter bar's geometry, grown upward.

import (
	"slices"
	"strings"
	"unicode"

	"github.com/sahilm/fuzzy"
)

// PaletteMaxRows is the most matches a drawer shows at once; the query
// narrows the rest into view.
const PaletteMaxRows = 8

// PaletteHelp is the help line every palette shows, so the keys read the
// same wherever the drawer opens.
const PaletteHelp = "enter run · esc close · ↑/↓ choose · type to narrow"

// PaletteItem is one command as the palette sees it: a name to match and
// draw, and the two hints beside it. Applicability, ordering and what it
// does are the caller's business — an item only reaches this slice if the
// caller already decided it applies.
type PaletteItem struct {
	// Title is the PascalCase command name, matched and drawn.
	Title string
	// Aliases are extra words the query may match, never drawn — the search
	// terms a title does not contain ("scan camera new card" for AddCards).
	Aliases string
	// Desc is the one-line explanation shown under the help line when this
	// item is the highlighted one. Optional.
	Desc string
	// Key is the shortcut this command also answers to, drawn dim and
	// right-aligned. Empty for commands the palette is the only way to run.
	Key string
	// Rank orders the empty-query list, highest first. Equal ranks keep the
	// order the caller supplied, so a registry's own sequence survives.
	Rank int
}

// PaletteMatch is one item that survived the query, with the rune positions
// the query matched in its title so they can be bolded.
type PaletteMatch struct {
	// Index is into the items slice passed to Refresh.
	Index     int
	Positions []int
}

// Palette is the drawer's state. The zero value is an open palette with an
// empty query; call Refresh before drawing it.
type Palette struct {
	Query   string
	Cursor  int
	matches []PaletteMatch
}

// SpacedTitle lowercases a PascalCase title into the words a search would
// type — "AddDeckByURL" → "add deck by url" — so spaced queries keep
// matching without every command restating its own name in aliases.
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

// Refresh recomputes the matches for the current query. Empty query: every
// item, ranked. Otherwise: fuzzy over title, spaced title and aliases,
// ordered by match quality.
func (p *Palette) Refresh(items []PaletteItem) {
	p.matches = p.matches[:0]

	if p.Query == "" {
		for i := range items {
			p.matches = append(p.matches, PaletteMatch{Index: i})
		}
		// Stable, so equal ranks keep the caller's order.
		slices.SortStableFunc(p.matches, func(a, b PaletteMatch) int {
			return items[b.Index].Rank - items[a.Index].Rank
		})
	} else {
		targets := make([]string, len(items))
		for i, it := range items {
			targets[i] = it.Title + " " + SpacedTitle(it.Title) + " " + it.Aliases
		}
		for _, fm := range fuzzy.Find(p.Query, targets) {
			// Only title positions bold: a match that landed in the aliases
			// has no rune on screen to mark.
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

// Matches is the current match list, in display order.
func (p *Palette) Matches() []PaletteMatch { return p.matches }

// Selected is the index into the caller's items slice for the highlighted
// row, and whether there is one.
func (p *Palette) Selected() (int, bool) {
	if len(p.matches) == 0 || p.Cursor >= len(p.matches) {
		return 0, false
	}
	return p.matches[p.Cursor].Index, true
}

// Up and Down move the cursor, stopping at the ends rather than wrapping —
// a list this short reads better bounded than cyclic.
func (p *Palette) Up()   { p.Cursor = max(p.Cursor-1, 0) }
func (p *Palette) Down() { p.Cursor = min(p.Cursor+1, max(len(p.matches)-1, 0)) }

// Type appends to the query. Backspace removes the last rune, Clear wipes
// it; all three leave Refresh to the caller, which owns the item list.
func (p *Palette) Type(s string) { p.Query += s }
func (p *Palette) Clear()        { p.Query = "" }
func (p *Palette) Backspace() {
	if p.Query == "" {
		return
	}
	r := []rune(p.Query)
	p.Query = string(r[:len(r)-1])
}

// Rows is how many drawer rows the palette costs the panes above it. At
// least one renders even with no match, so "no matching command" has
// somewhere to say itself.
func (p *Palette) Rows() int {
	return max(min(len(p.matches), PaletteMaxRows), 1)
}

// Lines renders the drawer's match rows at the given width.
func (p *Palette) Lines(items []PaletteItem, width int, th Theme) []string {
	if len(p.matches) == 0 {
		return []string{th.Help.Render("no matching command")}
	}

	// The window follows the cursor, same as the panes.
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

		// Title left, key hint right-aligned dim; the gap is measured on the
		// plain text so the bolding never shifts the column.
		plainWidth := 2 + len([]rune(it.Title))
		gap := max(width-plainWidth-len([]rune(it.Key))-1, 1)
		line := marker + BoldRunes(it.Title, match.Positions, th) +
			strings.Repeat(" ", gap) + th.Help.Render(it.Key)
		if i == p.Cursor {
			// The selection bar replaces the bolding rather than layering on
			// it: reverse video over bold is unreadable on half the terminals
			// that support both.
			line = th.Cursor.Render(marker+it.Title) +
				strings.Repeat(" ", gap) + th.Help.Render(it.Key)
		}
		lines = append(lines, Truncate(line, width))
	}
	return lines
}

// Desc is the highlighted item's one-line explanation, for the row under
// the help line. Empty when nothing matches or the item carries none.
func (p *Palette) Desc(items []PaletteItem) string {
	i, ok := p.Selected()
	if !ok {
		return ""
	}
	return items[i].Desc
}

// BoldRunes bolds the given rune positions of s — the palette's only
// styling, per the bold/dim-only rule.
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
