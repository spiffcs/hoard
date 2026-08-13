package decksource

import (
	"os"
	"strings"
	"testing"
)

// The Archidekt *text* export, from real bytes.
//
// testdata/archidekt-text.txt is lifted line for line out of an export found in
// the wild, trailing spaces and doubled spaces included, because every defect
// this covers lived in the punctuation. It carries one of each shape the real
// 180-line file contained: a "#" banner, a plain line with no annotation, a
// category, a category holding a slash and a comma, a foil marker sitting
// *before* the category, a {noDeck} maybeboard line with a colour label, and
// the commander as "[Commander{top}]".
//
// Before the annotations were stripped, this file did not merely misparse: the
// bracket landed in lineRE's collector-number group, went to Scryfall as a
// collector number, and returned 400 for the whole batch — so the deck imported
// as nothing at all. That is why the assertion here is on identifiers as well
// as boards; a name-only fallback would look like a pass while quietly giving
// up the printing.
func TestArchidektTextExportParses(t *testing.T) {
	f, err := os.Open("testdata/archidekt-text.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	d, err := ParseText("Fun With Fungus", "", "", "archidekt", f)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if len(d.Skipped) != 0 {
		t.Errorf("skipped %v, want every line read", d.Skipped)
	}

	type got struct {
		Name   string
		Set    string
		Number string
		Finish string
		Board  string
	}
	want := []got{
		{"Arcane Signet", "c21", "", "nonfoil", BoardMain},
		{"Atla Palani, Nest Tender", "c19", "", "foil", BoardMain},
		// {noDeck} — set aside by the owner, not part of the deck.
		{"Austere Command", "cmr", "", "nonfoil", BoardMaybe},
		{"Battlefield Forge", "ori", "", "nonfoil", BoardMain},
		{"Gishath, Sun's Avatar", "sld", "", "nonfoil", BoardCommander},
	}
	if len(d.Entries) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(d.Entries), len(want), d.Entries)
	}
	for i, w := range want {
		e := d.Entries[i]
		g := got{e.Name, e.Ident.Set, e.Ident.CollectorNumber, e.Finish, e.Board}
		if g != w {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
		// The export carries no collector number, so a set-only line must
		// resolve by name rather than inventing one.
		if e.Ident.CollectorNumber == "" && e.Ident.Name == "" {
			t.Errorf("entry %d has neither a collector number nor a name to resolve by", i)
		}
	}
}

// The annotations must not reach the card name, which is what was being sent to
// Scryfall. Checked on the stripping directly so a regression names the cause
// rather than surfacing three tests away as an unresolved card.
func TestArchidektAnnotationsAreLiftedOffTheLine(t *testing.T) {
	for _, tc := range []struct {
		line     string
		wantRest string
		wantCats []string
		wantNo   bool
	}{
		{"1x Arcane Signet (c21) [Rock/Dork,Artifact] ", "1x Arcane Signet (c21)", []string{"Rock/Dork", "Artifact"}, false},
		{"1x Austere Command (cmr) [Maybeboard{noDeck}{noPrice}]  ^Getting,#2ccce4^", "1x Austere Command (cmr)", []string{"Maybeboard{noDeck}{noPrice}"}, true},
		{"1x Gishath, Sun's Avatar (sld) [Commander{top}] ", "1x Gishath, Sun's Avatar (sld)", []string{"Commander{top}"}, false},
		// Untouched: no brackets, and a comma in the name is not a category.
		{"1 Borrowing 100,000 Arrows", "1 Borrowing 100,000 Arrows", nil, false},
		{"1 Sol Ring (C21) 125", "1 Sol Ring (C21) 125", nil, false},
	} {
		rest, cats, noDeck := splitArchidektAnnotations(tc.line)
		if rest != tc.wantRest {
			t.Errorf("rest of %q = %q, want %q", tc.line, rest, tc.wantRest)
		}
		if len(cats) != len(tc.wantCats) {
			t.Errorf("categories of %q = %v, want %v", tc.line, cats, tc.wantCats)
			continue
		}
		for i := range cats {
			if cats[i] != tc.wantCats[i] {
				t.Errorf("categories of %q = %v, want %v", tc.line, cats, tc.wantCats)
				break
			}
		}
		if noDeck != tc.wantNo {
			t.Errorf("noDeck of %q = %v, want %v", tc.line, noDeck, tc.wantNo)
		}
	}
}

// A custom category under a section header must not drag the card back to the
// main deck. This is the failure the (board, named) pair exists to prevent, and
// it is invisible in any single-section list.
func TestArchidektCustomCategoryLeavesTheSectionStanding(t *testing.T) {
	d, err := ParseText("x", "", "", "archidekt", strings.NewReader(
		"Sideboard\n1x Duress (m21) [Discard,Sorcery]\n"))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if len(d.Entries) != 1 || d.Entries[0].Board != BoardSide {
		t.Errorf("entries = %+v, want the card left on the sideboard", d.Entries)
	}
}
