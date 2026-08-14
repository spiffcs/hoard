package ui

import "testing"

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

	if a, b := its[p.Matches()[1].Index].Title, its[p.Matches()[2].Index].Title; a != "Scan" || b != "Pair" {
		t.Errorf("equal ranks reordered: got %q then %q", a, b)
	}
}

func TestQueryNarrowsAndMatchesAliases(t *testing.T) {
	var p Palette
	its := items()

	p.Query = "camera"
	p.Refresh(its)
	if len(p.Matches()) != 1 {
		t.Fatalf("matches = %d, want 1", len(p.Matches()))
	}
	m := p.Matches()[0]
	if its[m.Index].Title != "Scan" {
		t.Errorf("matched %q, want Scan", its[m.Index].Title)
	}

	for _, at := range m.Positions {
		if at >= len([]rune(its[m.Index].Title)) {
			t.Errorf("position %d escapes the title %q", at, its[m.Index].Title)
		}
	}
}

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
