package decksource

import (
	"os"
	"strings"
	"testing"
)

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

		{"Austere Command", "cmr", "", "nonfoil", BoardMaybe},
		{"Battlefield Forge", "ori", "", "nonfoil", BoardMain},
		{"Gishath, Sun's Avatar", "sld", "", "nonfoil", BoardCommander},
	}
	if len(d.Entries) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(d.Entries), len(want), d.Entries)
	}
	for i, w := range want {
		e := d.Entries[i]
		g := got{e.Name, e.Ident.Set, e.Ident.CollectorNumber, e.Finish.String(), e.Board}
		if g != w {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}

		if e.Ident.CollectorNumber == "" && e.Ident.Name == "" {
			t.Errorf("entry %d has neither a collector number nor a name to resolve by", i)
		}
	}
}

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
