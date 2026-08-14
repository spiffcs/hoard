package decksource

import (
	"os"
	"strings"
	"testing"
)

func TestDeckstatsExportSections(t *testing.T) {
	for _, tc := range []struct {
		file  string
		want  map[string]int
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
