package decksource

import (
	"os"
	"strings"
	"testing"
)

// Deckstats' text export, from real bytes.
//
// Both fixtures are excerpts of the same deck exported twice — once with the
// owner's categories, once without — which is the pairing that matters: the two
// files must produce the same hoard deck, and the categorised one is where the
// sectioning goes wrong. The full exports were 125 and 103 lines and agreed on
// 99 main plus 1 commander only after both bugs below were fixed.
//
// Deckstats spells a category as a comment, "//Commander" with the name against
// the slashes. hoard skipped every "//" line outright, so the commander sat in
// the main deck; and once "//Commander" was honoured, the twelve categories
// after it named no board, left the section standing, and filed all 100 cards
// under Commander instead. A fixture with only one category cannot see that
// second failure, which is why this one carries two.
func TestDeckstatsExportSections(t *testing.T) {
	for _, tc := range []struct {
		file  string
		want  map[string]int // board -> cards
		names map[string]string
	}{
		{
			file:  "testdata/deckstats-categories.txt",
			want:  map[string]int{BoardCommander: 1, BoardMain: 2},
			names: map[string]string{BoardCommander: "The Mimeoplasm"},
		},
		{
			file:  "testdata/deckstats-plain.txt",
			want:  map[string]int{BoardCommander: 1, BoardMain: 2},
			names: map[string]string{BoardCommander: "The Mimeoplasm"},
		},
	} {
		t.Run(strings.TrimSuffix(strings.TrimPrefix(tc.file, "testdata/deckstats-"), ".txt"), func(t *testing.T) {
			f, err := os.Open(tc.file)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer f.Close()

			d, err := ParseText("Mimeoplasm", "", "", "deckstats", f)
			if err != nil {
				t.Fatalf("ParseText: %v", err)
			}
			if len(d.Skipped) != 0 {
				t.Errorf("skipped %v, want every line read", d.Skipped)
			}
			got := map[string]int{}
			for _, e := range d.Entries {
				got[e.Board] += e.Quantity
			}
			for board, n := range tc.want {
				if got[board] != n {
					t.Errorf("board %q holds %d, want %d (all: %v)", board, got[board], n, got)
				}
			}
			for _, e := range d.Entries {
				if want, ok := tc.names[e.Board]; ok && e.Name != want {
					t.Errorf("board %q holds %q, want %q", e.Board, e.Name, want)
				}
			}
		})
	}
}

// A split card's name contains the comment marker. It must not be mistaken for
// one — the line does not begin with it — and the set and number on the line
// still address the printing exactly, so no front-face retry is needed.
func TestDeckstatsSplitCardIsNotAComment(t *testing.T) {
	d, err := ParseText("x", "", "", "deckstats",
		strings.NewReader("//Main\n1 Life // Death (DMR) 216\n"))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if len(d.Entries) != 1 {
		t.Fatalf("entries = %+v, want the split card", d.Entries)
	}
	e := d.Entries[0]
	if e.Name != "Life // Death" || e.Ident.Set != "dmr" || e.Ident.CollectorNumber != "216" {
		t.Errorf("entry = %+v, want Life // Death addressed by dmr/216", e)
	}
}

// A written comment keeps its old meaning. The space after the slashes is what
// separates a note from a Deckstats category, so a note must not silently end
// the section it sits in.
func TestWrittenCommentDoesNotEndASection(t *testing.T) {
	d, err := ParseText("x", "", "", "",
		strings.NewReader("Sideboard\n1 Duress (m21) 95\n// a note to self\n1 Negate (m21) 55\n"))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if len(d.Entries) != 2 {
		t.Fatalf("entries = %+v, want both cards", d.Entries)
	}
	for _, e := range d.Entries {
		if e.Board != BoardSide {
			t.Errorf("%q landed on %q, want the sideboard it was written under", e.Name, e.Board)
		}
	}
}
