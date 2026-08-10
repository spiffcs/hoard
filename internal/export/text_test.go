package export

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/decksource"
)

// deckFixture is one deck as ExportRows would hand it over: rows out of order,
// spread across boards, in all three finishes, and with the same printing held
// in two conditions — which the canonical CSV keeps apart and a decklist
// cannot.
func deckFixture() []Row {
	return []Row{
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125", Finish: "foil",
			ScryfallID: "sol-1", Container: "Fish", Kind: "deck", Board: "main", Condition: "nm"},
		{Count: 1, Name: "Atraxa, Praetors' Voice", Set: "c16", CollectorNumber: "28",
			Finish: "nonfoil", ScryfallID: "atx-1", Container: "Fish", Kind: "deck", Board: "commander"},
		{Count: 2, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78", Finish: "nonfoil",
			ScryfallID: "rem-1", Container: "Fish", Kind: "deck", Board: "main", Condition: "nm"},
		{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78", Finish: "nonfoil",
			ScryfallID: "rem-1", Container: "Fish", Kind: "deck", Board: "main", Condition: "mp"},
		{Count: 1, Name: "Aether Vial", Set: "dst", CollectorNumber: "91", Finish: "etched",
			ScryfallID: "vial-1", Container: "Fish", Kind: "deck", Board: "side"},
	}
}

func TestWriteTextGroupsBoardsAndMarksFinishes(t *testing.T) {
	var b strings.Builder
	if err := WriteText(&b, deckFixture()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	// The two Remora rows merge into 3: their conditions are the only thing
	// separating them and this grammar has no cell for one.
	want := strings.Join([]string{
		"Deck",
		"3 Mystic Remora (ice) 78",
		"1 Sol Ring (c21) 125 *F*",
		"",
		"Commander",
		"1 Atraxa, Praetors' Voice (c16) 28",
		"",
		"Sideboard",
		"1 Aether Vial (dst) 91 *E*",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("text decklist:\n%s\nwant:\n%s", b.String(), want)
	}
}

// The point of the format: decksource/textlist.go — the reader `deck add
// --file` uses — must take this file back with every card, count, printing,
// finish and board intact. Asserting against the real parser rather than a
// restatement of its regex is what stops the writer drifting into a near-miss
// of a grammar it does not own.
func TestWriteTextIsReadBackByTheDeckReader(t *testing.T) {
	var b strings.Builder
	if err := WriteText(&b, deckFixture()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	deck, err := decksource.ParseText("Fish", "", "", "", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseText of our own output: %v\n%s", err, b.String())
	}
	if len(deck.Skipped) > 0 {
		t.Errorf("the reader could not read %d of our lines: %v", len(deck.Skipped), deck.Skipped)
	}

	type got struct {
		set, number, finish, board string
		qty                        int
	}
	byName := make(map[string]got, len(deck.Entries))
	for _, e := range deck.Entries {
		byName[e.Name] = got{e.Ident.Set, e.Ident.CollectorNumber, e.Finish, e.Board, e.Quantity}
	}
	for name, want := range map[string]got{
		"Mystic Remora":           {"ice", "78", "nonfoil", decksource.BoardMain, 3},
		"Sol Ring":                {"c21", "125", "foil", decksource.BoardMain, 1},
		"Atraxa, Praetors' Voice": {"c16", "28", "nonfoil", decksource.BoardCommander, 1},
		"Aether Vial":             {"dst", "91", "etched", decksource.BoardSide, 1},
	} {
		if byName[name] != want {
			t.Errorf("%s read back as %+v, want %+v", name, byName[name], want)
		}
	}
	if len(deck.Entries) != 4 {
		t.Errorf("entries = %d, want 4", len(deck.Entries))
	}
}

// A card whose own name ends in a parenthetical still pins its printing: the
// reader's set group takes alphanumerics only, so the name keeps its brackets
// and the trailing pair is still read as set and number. Erase is a real
// printing and the reason this is worth a test.
func TestWriteTextSurvivesAParentheticalCardName(t *testing.T) {
	var b strings.Builder
	rows := []Row{{Count: 1, Name: "Erase (Not the Urza's Legacy One)", Set: "uds",
		CollectorNumber: "43", Finish: "nonfoil", Container: "Fish", Kind: "deck", Board: "main"}}
	if err := WriteText(&b, rows); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	deck, err := decksource.ParseText("Fish", "", "", "", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	e := deck.Entries[0]
	if e.Name != "Erase (Not the Urza's Legacy One)" || e.Ident.Set != "uds" || e.Ident.CollectorNumber != "43" {
		t.Errorf("read back as name=%q set=%q number=%q", e.Name, e.Ident.Set, e.Ident.CollectorNumber)
	}
}

// A set code the reader's grammar cannot express is left off rather than
// written into a line that parses as a different card's name. The bare name
// still resolves; a mangled name does not.
func TestWriteTextOmitsAnUnreadableSetCode(t *testing.T) {
	var b strings.Builder
	rows := []Row{
		{Count: 1, Name: "Sol Ring", Set: "c-21", CollectorNumber: "125", Finish: "nonfoil", Board: "main"},
		{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "", Finish: "nonfoil", Board: "main"},
	}
	if err := WriteText(&b, rows); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	want := "1 Mystic Remora\n1 Sol Ring\n"
	if b.String() != want {
		t.Errorf("text decklist:\n%s\nwant:\n%s", b.String(), want)
	}
	deck, err := decksource.ParseText("Fish", "", "", "", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	for _, e := range deck.Entries {
		if e.Ident.Name == "" {
			t.Errorf("%s fell back to no identifier at all: %+v", e.Name, e.Ident)
		}
	}
}

// A main-only list is written bare, because that is what a person pastes into
// a deck builder — and the reader starts every file in main anyway.
func TestWriteTextLeavesAMainOnlyListUnheadered(t *testing.T) {
	var b strings.Builder
	rows := []Row{{Count: 4, Name: "Lightning Bolt", Set: "2x2", CollectorNumber: "117",
		Finish: "nonfoil", Board: "main"}}
	if err := WriteText(&b, rows); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if b.String() != "4 Lightning Bolt (2x2) 117\n" {
		t.Errorf("main-only list:\n%s", b.String())
	}
}

// A board with no header of its own — a stored value from some future source,
// or a typo — folds into main instead of being written as a header the reader
// would drop, taking the cards under it along.
func TestWriteTextFoldsAnUnknownBoardIntoMain(t *testing.T) {
	var b strings.Builder
	rows := []Row{{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125",
		Finish: "nonfoil", Board: "wishboard"}}
	if err := WriteText(&b, rows); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if strings.Contains(b.String(), "wishboard") {
		t.Errorf("an unknown board was written as a header:\n%s", b.String())
	}
	deck, err := decksource.ParseText("Fish", "", "", "", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if len(deck.Entries) != 1 || deck.Entries[0].Board != decksource.BoardMain {
		t.Errorf("entries = %+v, want the card in main", deck.Entries)
	}
}

// Same promise the CSV writers make: the caller's rows come back untouched.
func TestWriteTextDoesNotReorderTheCallersSlice(t *testing.T) {
	rows := deckFixture()
	first := rows[0]
	var b strings.Builder
	if err := WriteText(&b, rows); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if !reflect.DeepEqual(rows[0], first) {
		t.Errorf("caller's slice was mutated: rows[0] = %+v", rows[0])
	}
}
