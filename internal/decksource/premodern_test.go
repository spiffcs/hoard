package decksource

import (
	"os"
	"testing"
)

func TestParsesPremodernMagicExport(t *testing.T) {
	f, err := os.Open("testdata/premodernmagic.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	d, err := ParseText("", "", "", "premodernmagic", f)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}

	if d.Name != "Deadguy Ale" {
		t.Errorf("deck name = %q, want the title the file declares", d.Name)
	}

	boards := map[string]int{}
	for _, e := range d.Entries {
		boards[e.Board] += e.Quantity
	}
	if boards[BoardMain] != 60 {
		t.Errorf("maindeck holds %d cards, want the 60 the file declares", boards[BoardMain])
	}
	if boards[BoardSide] != 15 {
		t.Errorf("sideboard holds %d cards, want the 15 the file declares", boards[BoardSide])
	}

	qty := func(board, name string) int {
		for _, e := range d.Entries {
			if e.Board == board && e.Name == name {
				return e.Quantity
			}
		}
		return 0
	}
	for _, want := range []struct {
		board, name string
		n           int
	}{
		{BoardMain, "Dark Ritual", 4},
		{BoardMain, "Hypnotic Specter", 4},
		{BoardMain, "Phyrexian Arena", 2},
		{BoardMain, "Swamp", 10},
		{BoardMain, "Wasteland", 4},
		{BoardSide, "Withered Wretch", 3},
		{BoardSide, "Engineered Plague", 2},
	} {
		if got := qty(want.board, want.name); got != want.n {
			t.Errorf("%s %s = %d, want %d", want.board, want.name, got, want.n)
		}
	}

	if main, side := qty(BoardMain, "Diabolic Edict"), qty(BoardSide, "Diabolic Edict"); main != 1 || side != 1 {
		t.Errorf("Diabolic Edict is 1 main and 1 side, got %d and %d", main, side)
	}

	if len(d.Skipped) != 0 {
		t.Errorf("a format hoard supports should read every line; %d were unreadable: %v",
			len(d.Skipped), d.Skipped)
	}
}

func TestDeckNameFallsBackWhenThereIsNoTitle(t *testing.T) {
	premodern := func(t *testing.T, given string) string {
		t.Helper()
		f, err := os.Open("testdata/premodernmagic.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		d, err := ParseText(given, "", "", "premodernmagic", f)
		if err != nil {
			t.Fatalf("ParseText: %v", err)
		}
		return d.Name
	}
	if got := premodern(t, "Chosen By Hand"); got != "Chosen By Hand" {
		t.Errorf("an explicit name must win over the file title, got %q", got)
	}

	f, err := os.Open("testdata/deckstats-plain.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseText("", "", "", "text", f)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if d.Name != "" {
		t.Errorf("a file with no title must leave the name empty for the caller to fill "+
			"from the filename, got %q", d.Name)
	}
}
