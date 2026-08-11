package watchsource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func parseFile(t *testing.T, name string) []Row {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return rows
}

// Both fixtures describe the same three watches; whichever format carried
// them, the parsed rows must agree.
func checkFixtureRows(t *testing.T, rows []Row) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	sol := rows[0]
	if sol.Name != "Sol Ring" || sol.Op != "under" || sol.Threshold != 1.5 || sol.Finish != "nonfoil" {
		t.Errorf("Sol Ring row = %+v", sol)
	}
	if sol.Ident != (scryfall.Identifier{Set: "c21", CollectorNumber: "263"}) {
		t.Errorf("set+number should beat name: ident = %+v", sol.Ident)
	}
	sheoldred := rows[1]
	if sheoldred.Name != "Sheoldred, the Apocalypse" || sheoldred.Finish != "foil" || sheoldred.Threshold != 40 {
		t.Errorf("Sheoldred row = %+v", sheoldred)
	}
	if sheoldred.Ident != (scryfall.Identifier{Name: "Sheoldred, the Apocalypse"}) {
		t.Errorf("bare name should resolve by name: ident = %+v", sheoldred.Ident)
	}
	if bow := rows[2]; bow.Op != "over" || bow.Threshold != 25 {
		t.Errorf("Orcish Bowmasters row = %+v", bow)
	}
}

func TestParseCSVFixture(t *testing.T)  { checkFixtureRows(t, parseFile(t, "watches.csv")) }
func TestParseJSONFixture(t *testing.T) { checkFixtureRows(t, parseFile(t, "watches.json")) }

func TestSniffJSONByLeadingBracket(t *testing.T) {
	// Whitespace and a BOM before the bracket must not defeat the sniff.
	rows, err := Parse([]byte("\ufeff\n [\n{\"name\":\"Sol Ring\",\"direction\":\"under\",\"thresholdUsd\":2}]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Sol Ring" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCSVTolerations(t *testing.T) {
	// BOM, reordered and extra columns, case-insensitive headers, $ and
	// truthy foil spellings, ragged rows, scryfall id beating set+number.
	csv := "\ufeffthreshold,NAME,Extra,direction,finish,scryfall id,set,collector number\n" +
		"$12.50,Ragavan,x,under,true,abc-123,mom,42\n" +
		"3,Fury,,over\n"
	rows, err := Parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if r := rows[0]; r.Threshold != 12.5 || r.Finish != "foil" || r.Ident != (scryfall.Identifier{ID: "abc-123"}) {
		t.Errorf("row 1 = %+v", r)
	}
	if r := rows[1]; r.Op != "over" || r.Finish != "nonfoil" || r.Ident != (scryfall.Identifier{Name: "Fury"}) {
		t.Errorf("row 2 = %+v", r)
	}
}

func TestEtchedFinishPassesThrough(t *testing.T) {
	rows, err := Parse([]byte("Name,Direction,Threshold,Finish\nSol Ring,under,5,etched\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Finish != "etched" {
		t.Errorf("finish = %q, want etched (the resolver corrects it to a price finish)", rows[0].Finish)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", "empty file"},
		{"blank", "  \n ", "empty file"},
		{"missing column", "Name,Threshold\nSol Ring,5\n", `watch CSV is missing its "Direction" column (saw: Name, Threshold)`},
		{"no name", "Name,Direction,Threshold\n,under,5\n", "line 2: no card name"},
		{"bad direction", "Name,Direction,Threshold\nSol Ring,below,5\n", `line 2 (Sol Ring): direction must be under, over, drop or rise, not "below"`},
		{"bad threshold", "Name,Direction,Threshold\nSol Ring,under,cheap\n", `line 2 (Sol Ring): cannot parse threshold "cheap"`},
		{"zero threshold", "Name,Direction,Threshold\nSol Ring,under,0\n", "line 2 (Sol Ring): threshold must be a positive dollar amount"},
		// A comma inside an unquoted card name shifts every column after it,
		// so the direction cell reads a fragment of the name. Short rows stay
		// tolerated (TestCSVTolerations); only over-long ones are refused,
		// because nothing but an unquoted delimiter can produce one.
		{"overlong row", "Name,Direction,Threshold\nShalai, Voice of Plenty,under,5\n",
			"line 2: 4 fields, header has 3 — an unquoted comma in a card name?"},
		{"header only", "Name,Direction,Threshold\n", "no watches found in file"},
		{"not a list", "[{\"name\": 5}]", "not a watch list: want a JSON array of {name, direction, thresholdUsd} objects"},
		{"json no name", `[{"direction":"under","thresholdUsd":5}]`, "entry 1: no card name"},
		{"json bad direction", `[{"name":"Sol Ring","direction":"below","thresholdUsd":5}]`, `entry 1 (Sol Ring): direction must be under, over, drop or rise, not "below"`},
		{"json no threshold", `[{"name":"Sol Ring","direction":"under"}]`, "entry 1 (Sol Ring): threshold must be a positive dollar amount"},
		{"json empty array", "[]", "no watches found in file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
