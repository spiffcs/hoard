package ui

import "testing"

// The search targets a palette builds from a title, so a spaced query keeps
// matching without every command restating its own name in aliases.
func TestSpacedTitle(t *testing.T) {
	for title, want := range map[string]string{
		"AddCards":               "add cards",
		"AddDeckByURL":           "add deck by url",
		"ImportCollectionCSV":    "import collection csv",
		"BackfillPriceHistory30": "backfill price history 30",
		"SetPennyFilter":         "set penny filter",
		"":                       "",
	} {
		if got := SpacedTitle(title); got != want {
			t.Errorf("SpacedTitle(%q) = %q, want %q", title, got, want)
		}
	}
}

func items() []PaletteItem {
	return []PaletteItem{
		{Title: "Scan", Aliases: "camera iphone", Desc: "Scan with your iPhone.", Key: "ctrl+o"},
		{Title: "Pair", Aliases: "phone code", Desc: "Pair a phone.", Key: "ctrl+p"},
		{Title: "Done", Aliases: "finish exit", Desc: "Finish adding.", Key: "ctrl+d", Rank: 5},
	}
}

// An empty query lists everything, highest rank first, and equal ranks keep
// the order the caller supplied.
func TestEmptyQueryRanksAndKeepsOrder(t *testing.T) {
	var p Palette
	its := items()
	p.Refresh(its)
	if len(p.Matches()) != 3 {
		t.Fatalf("matches = %d, want 3", len(p.Matches()))
	}
	if got := its[p.Matches()[0].Index].Title; got != "Done" {
		t.Errorf("top row = %q, want Done (rank 5)", got)
	}
	// Scan and Pair both rank zero, so the registry order decides.
	if a, b := its[p.Matches()[1].Index].Title, its[p.Matches()[2].Index].Title; a != "Scan" || b != "Pair" {
		t.Errorf("equal ranks reordered: got %q then %q", a, b)
	}
}

// A query narrows, and matching on an alias still selects the command even
// though no rune of the title is bolded.
func TestQueryNarrowsAndMatchesAliases(t *testing.T) {
	var p Palette
	its := items()
	// "camera" and not "iphone": fuzzy matching is a subsequence test, and
	// i-p-h-o-n-e is a subsequence of "Pair … phone" too.
	p.Query = "camera"
	p.Refresh(its)
	if len(p.Matches()) != 1 {
		t.Fatalf("matches = %d, want 1", len(p.Matches()))
	}
	m := p.Matches()[0]
	if its[m.Index].Title != "Scan" {
		t.Errorf("matched %q, want Scan", its[m.Index].Title)
	}
	// Positions are title-relative and must stay inside it: the match ran
	// against title + spaced title + aliases, and a position past the title's
	// last rune would index off the end of the string being bolded.
	for _, at := range m.Positions {
		if at >= len([]rune(its[m.Index].Title)) {
			t.Errorf("position %d escapes the title %q", at, its[m.Index].Title)
		}
	}
}

// The cursor stops at the ends rather than wrapping, and a narrowing query
// pulls it back into range rather than leaving it past the last row.
func TestCursorStaysInRange(t *testing.T) {
	var p Palette
	its := items()
	p.Refresh(its)
	p.Up()
	if p.Cursor != 0 {
		t.Errorf("cursor went above the first row: %d", p.Cursor)
	}
	for range 5 {
		p.Down()
	}
	if p.Cursor != 2 {
		t.Errorf("cursor ran past the last row: %d", p.Cursor)
	}
	p.Query = "scan"
	p.Refresh(its)
	if p.Cursor != 0 {
		t.Errorf("narrowing left the cursor at %d, past the only match", p.Cursor)
	}
}

// No match still costs one row, so the drawer has somewhere to say so.
func TestNoMatchStillDrawsARow(t *testing.T) {
	var p Palette
	its := items()
	p.Query = "zzzz"
	p.Refresh(its)
	if len(p.Matches()) != 0 {
		t.Fatalf("nonsense query matched %d", len(p.Matches()))
	}
	if p.Rows() != 1 {
		t.Errorf("rows = %d, want 1", p.Rows())
	}
	if _, ok := p.Selected(); ok {
		t.Error("nothing matched, so nothing is selected")
	}
	if got := p.Desc(its); got != "" {
		t.Errorf("desc = %q, want empty", got)
	}
}

// Typing and erasing drive the query, and the drawer never grows past its
// row cap however many commands apply.
func TestTypingAndRowCap(t *testing.T) {
	var p Palette
	p.Type("sc")
	p.Type("a")
	p.Backspace()
	if p.Query != "sc" {
		t.Errorf("query = %q, want %q", p.Query, "sc")
	}
	p.Clear()
	if p.Query != "" {
		t.Errorf("clear left %q", p.Query)
	}

	many := make([]PaletteItem, PaletteMaxRows+4)
	for i := range many {
		many[i] = PaletteItem{Title: "Cmd"}
	}
	p.Refresh(many)
	if p.Rows() != PaletteMaxRows {
		t.Errorf("rows = %d, want the cap %d", p.Rows(), PaletteMaxRows)
	}
}
