package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/cphillips918/hoard/internal/store"
	"github.com/cphillips918/hoard/internal/ui"
)

func TestExtractDBFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRest []string
		wantDB   string
	}{
		{"absent", []string{"summary"}, []string{"summary"}, ""},
		{"before the command", []string{"--db", "x.db", "summary"}, []string{"summary"}, "x.db"},
		// The case this function exists for: previously silently ignored.
		{"after the command", []string{"summary", "--db", "x.db"}, []string{"summary"}, "x.db"},
		{"equals form", []string{"summary", "--db=x.db"}, []string{"summary"}, "x.db"},
		{"single dash", []string{"-db", "x.db", "list"}, []string{"list"}, "x.db"},
		{"repeated keeps the last", []string{"--db", "a.db", "list", "--db", "b.db"}, []string{"list"}, "b.db"},
		{
			"subcommand flags are left alone",
			[]string{"add", "--qty", "2", "--db", "x.db", "--foil", "url"},
			[]string{"add", "--qty", "2", "--foil", "url"}, "x.db",
		},
		{
			"a value that looks like a flag is still a value",
			[]string{"--db", "--weird.db", "list"},
			[]string{"list"}, "--weird.db",
		},
		{
			"nothing is extracted after a bare --",
			[]string{"add", "--", "--db", "x.db"},
			[]string{"add", "--", "--db", "x.db"}, "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rest, db, err := extractDBFlag(c.args)
			if err != nil {
				t.Fatalf("extractDBFlag(%q): %v", c.args, err)
			}
			if db != c.wantDB {
				t.Errorf("db = %q, want %q", db, c.wantDB)
			}
			if !slices.Equal(rest, c.wantRest) {
				t.Errorf("rest = %q, want %q", rest, c.wantRest)
			}
		})
	}
}

// A --db with no usable value must fail loudly rather than fall back to the
// default database, which is the whole point of handling it explicitly.
func TestExtractDBFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"summary", "--db"},
		{"--db"},
		{"summary", "--db="},
		{"summary", "--db", ""},
	} {
		if _, _, err := extractDBFlag(args); err == nil {
			t.Errorf("extractDBFlag(%q) = nil error, want one", args)
		}
	}
}

func testCollection() store.CollectionTotals {
	return store.CollectionTotals{DistinctCards: 2, TotalCopies: 10, Value: 100}
}

// Deliberately supplied out of order, and with one unpriced deck, so the sort
// and the zero-value bar are both exercised.
func testDecks() []store.DeckSummary {
	mk := func(name string, copies int, value float64) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: copies, Value: value}
		d.Name = name
		d.Kind = store.KindDeck
		return d
	}
	return []store.DeckSummary{
		mk("Beta Deck (Precon)", 60, 100),
		mk("Unpriced Deck (Precon)", 40, 0),
		mk("Alpha Deck (Precon)", 100, 300),
	}
}

func renderSummary(env ui.Env) string {
	return summaryTable(env, testCollection(), testDecks()).Render()
}

func TestSummaryTable(t *testing.T) {
	got := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	want := strings.Join([]string{
		"COLLECTION                 10  $100.00  ██",
		"DECKS · 3                 200  $400.00  ████████",
		"",
		"  Alpha Deck (Precon)     100  $300.00  ██████",
		"  Beta Deck (Precon)       60  $100.00  ██",
		"  Unpriced Deck (Precon)   40    $0.00",
		"",
		"TOTAL                     210  $500.00",
		"",
	}, "\n")
	if got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}
}

// Decks descend by value; the collection and decks sections must sum to TOTAL.
func TestSummaryTableSortsByValue(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	alpha := strings.Index(out, "Alpha")
	beta := strings.Index(out, "Beta")
	unpriced := strings.Index(out, "Unpriced")
	if !(alpha < beta && beta < unpriced) {
		t.Errorf("decks not ordered by value desc:\n%s", out)
	}
}

// A tie in value falls back to name order, so unpriced hoards don't shuffle.
func TestSummaryTableTieBreaksByName(t *testing.T) {
	mk := func(name string) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: 1}
		d.Name = name
		return d
	}
	decks := []store.DeckSummary{mk("Zeta"), mk("Alpha"), mk("Mid")}
	out := summaryTable(ui.Env{Width: 80, Clamp: true}, store.CollectionTotals{}, decks).Render()
	a, m, z := strings.Index(out, "Alpha"), strings.Index(out, "Mid"), strings.Index(out, "Zeta")
	if !(a < m && m < z) {
		t.Errorf("equal-value decks not name-ordered:\n%s", out)
	}
}

func TestDecksByValue(t *testing.T) {
	mk := func(name string, value float64) store.DeckSummary {
		d := store.DeckSummary{Value: value}
		d.Name = name
		return d
	}
	in := []store.DeckSummary{
		mk("cheap", 1), mk("rich", 300), mk("zero-b", 0), mk("mid", 50), mk("zero-a", 0),
	}
	got := decksByValue(in)

	want := []string{"rich", "mid", "cheap", "zero-a", "zero-b"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, got[i].Name, name, names(got))
		}
	}

	// The caller's slice must be left alone: cmdDeckList and summaryTable both
	// sort the same result of ListDecks.
	if in[0].Name != "cheap" {
		t.Errorf("decksByValue mutated its argument: %v", names(in))
	}
}

func TestCollectionByValue(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	mk := func(name string, normal int, usd *float64, foil int, usdFoil *float64) store.CollectionCard {
		c := store.CollectionCard{QtyNormal: normal, QtyFoil: foil}
		c.Name = name
		c.PriceUSD = usd
		c.PriceUSDFoil = usdFoil
		return c
	}
	in := []store.CollectionCard{
		mk("cheap", 1, price(1), 0, nil),
		// Quantity counts: 10 × $5 outranks a single $30 card.
		mk("bulk-but-many", 10, price(5), 0, nil),
		mk("unpriced-b", 3, nil, 0, nil),
		mk("one-expensive", 1, price(30), 0, nil),
		mk("unpriced-a", 1, nil, 0, nil),
		// Foil value must be counted alongside normal.
		mk("foil-heavy", 0, nil, 2, price(60)),
	}
	got := collectionByValue(in)

	want := []string{"foil-heavy", "bulk-but-many", "one-expensive", "cheap", "unpriced-a", "unpriced-b"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, got[i].Name, name, cardNames(got))
		}
	}
	if in[0].Name != "cheap" {
		t.Errorf("collectionByValue mutated its argument: %v", cardNames(in))
	}
}

func TestEntriesByValue(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	mk := func(name, board, finish string, qty int, usd, usdFoil *float64) store.EntryView {
		e := store.EntryView{Finish: finish, Board: board, Quantity: qty}
		e.Card.Name = name
		e.Card.PriceUSD = usd
		e.Card.PriceUSDFoil = usdFoil
		return e
	}
	in := []store.EntryView{
		// Grouped by board as the store returns them, cheapest first.
		mk("commander-cheap", "commander", "normal", 1, price(2), nil),
		mk("main-mid", "main", "normal", 1, price(40), nil),
		// Quantity counts: 10 x $9 beats one $40 card.
		mk("main-many", "main", "normal", 10, price(9), nil),
		// Foil entries take the foil price.
		mk("side-foil", "side", "foil", 1, price(1), price(75)),
		mk("side-unpriced", "side", "normal", 1, nil, nil),
	}
	got := entriesByValue(in)

	want := []string{"main-many", "side-foil", "main-mid", "commander-cheap", "side-unpriced"}
	for i, name := range want {
		if got[i].Card.Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Card.Name, name)
		}
	}
	// Board grouping is deliberately flattened; the BOARD column still carries it.
	if got[0].Board != "main" || got[1].Board != "side" {
		t.Errorf("expected boards to interleave by value, got %q then %q",
			got[0].Board, got[1].Board)
	}
	if in[0].Card.Name != "commander-cheap" {
		t.Error("entriesByValue mutated its argument")
	}
}

func cardNames(cards []store.CollectionCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Name
	}
	return out
}

func names(decks []store.DeckSummary) []string {
	out := make([]string, len(decks))
	for i, d := range decks {
		out[i] = d.Name
	}
	return out
}

// The non-terminal profile is the scriptable one: whole names, no bars, and
// crucially no escape sequences.
func TestSummaryTablePiped(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: false, Bars: false})
	if strings.Contains(out, "\x1b") {
		t.Error("piped summary contains ANSI escapes")
	}
	if strings.ContainsAny(out, "█▏▎▍▌▋▊▉") {
		t.Error("piped summary contains share bars")
	}
	if strings.Contains(out, "…") {
		t.Error("piped summary truncated a name")
	}
	if !strings.Contains(out, "Unpriced Deck (Precon)") {
		t.Errorf("piped summary lost a full deck name:\n%s", out)
	}
}

// At a narrow width the layout must still fit exactly — that is the whole point
// of truncating rather than letting rows wrap and break the bar column.
func TestSummaryTableNarrow(t *testing.T) {
	for _, width := range []int{60, 50, 40, 30} {
		env := ui.Env{Width: width, Clamp: true, Bars: width >= 50}
		for line := range strings.SplitSeq(renderSummary(env), "\n") {
			if w := len([]rune(line)); w > width {
				t.Errorf("width %d: line %q is %d wide", width, line, w)
			}
		}
	}
}

// An empty hoard must render zeros rather than dividing by a zero grand total.
func TestSummaryTableEmpty(t *testing.T) {
	out := summaryTable(ui.Env{Width: 80, Clamp: true, Bars: true},
		store.CollectionTotals{}, nil).Render()
	want := strings.Join([]string{
		"COLLECTION  0  $0.00",
		"DECKS · 0   0  $0.00",
		"",
		"TOTAL       0  $0.00",
		"",
	}, "\n")
	if out != want {
		t.Errorf("empty summary =\n%s\nwant\n%s", out, want)
	}
}
