package decksource

import (
	"strings"
	"testing"
)

func TestParseTextBasic(t *testing.T) {
	in := `// My deck
Commander
1 Edgar Markov (MM3) 216

Deck
2 Sol Ring
1x Lightning Bolt
1 Ulamog, the Infinite Gyre (UMA) 7 *F*

Maybeboard
1 Mana Crypt
`
	d, err := ParseText("My EDH", "mm4NEsy4dEy9xxt5MU3s_A", "https://moxfield.com/decks/mm4NEsy4dEy9xxt5MU3s_A", "moxfield", strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if d.Source != "moxfield" || d.SourceID != "mm4NEsy4dEy9xxt5MU3s_A" {
		t.Errorf("metadata wrong: %+v", d)
	}
	if len(d.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(d.Entries))
	}

	// Commander line: set+number identifier, commander board.
	e := d.Entries[0]
	if e.Board != BoardCommander || e.Ident.Set != "mm3" || e.Ident.CollectorNumber != "216" || e.Quantity != 1 {
		t.Errorf("commander entry wrong: %+v", e)
	}
	// "2 Sol Ring" → name identifier, main board.
	if d.Entries[1].Ident.Name != "Sol Ring" || d.Entries[1].Quantity != 2 || d.Entries[1].Board != BoardMain {
		t.Errorf("sol ring entry wrong: %+v", d.Entries[1])
	}
	// "1x Lightning Bolt" → the x-form quantity.
	if d.Entries[2].Ident.Name != "Lightning Bolt" || d.Entries[2].Quantity != 1 {
		t.Errorf("bolt entry wrong: %+v", d.Entries[2])
	}
	// Ulamog: set+number and foil finish.
	u := d.Entries[3]
	if u.Ident.Set != "uma" || u.Ident.CollectorNumber != "7" || u.Finish != "foil" {
		t.Errorf("ulamog entry wrong: %+v", u)
	}
	// Maybeboard line.
	if d.Entries[4].Board != BoardMaybe || d.Entries[4].Ident.Name != "Mana Crypt" {
		t.Errorf("maybe entry wrong: %+v", d.Entries[4])
	}
}

func TestParseTextDefaultSourceID(t *testing.T) {
	d, err := ParseText("Loose Pile", "", "", "", strings.NewReader("1 Sol Ring\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != "text" || d.SourceID != "loose pile" {
		t.Errorf("defaults wrong: source=%q id=%q", d.Source, d.SourceID)
	}
}

func TestParseTextErrors(t *testing.T) {
	if _, err := ParseText("x", "", "", "", strings.NewReader("// only a comment\n")); err == nil {
		t.Error("expected error for empty decklist")
	}
	if _, err := ParseText("x", "", "", "", strings.NewReader("this is not a card line\n")); err == nil {
		t.Error("expected parse error for unrecognized line")
	}
}

// ParseLoose is the paste reader: headers vanish (a binder has no boards),
// bad lines are reported rather than fatal, and everything lands on main.
func TestParseLoose(t *testing.T) {
	in := `Deck
4 Sol Ring (C21) 125
what even is this line
1x Lightning Bolt *F*
Sideboard
2 Mystic Remora

// a comment
`
	entries, skipped, err := ParseLoose(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseLoose: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want 3", entries)
	}
	for _, e := range entries {
		if e.Board != BoardMain {
			t.Errorf("%s is on board %q, want main — a paste has no sections", e.Name, e.Board)
		}
	}
	if entries[0].Quantity != 4 || entries[0].Ident.Set != "c21" {
		t.Errorf("first entry = %+v", entries[0])
	}
	if entries[1].Finish != "foil" {
		t.Errorf("Bolt finish = %q, want the *F* marker honored", entries[1].Finish)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "line 3") {
		t.Errorf("skipped = %v, want the unreadable line reported with its number", skipped)
	}
}

func TestParseLooseEmpty(t *testing.T) {
	entries, skipped, err := ParseLoose(strings.NewReader("# nothing\n\n"))
	if err != nil || len(entries) != 0 || len(skipped) != 0 {
		t.Errorf("empty paste = %v, %v, %v", entries, skipped, err)
	}
}
