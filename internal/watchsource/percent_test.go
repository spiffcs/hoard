package watchsource

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func TestCSVPercentColumns(t *testing.T) {
	rows, err := Parse([]byte(
		"Name,Direction,Threshold,Percent,Min Move,Since\n" +
			"Sol Ring,under,5,,,\n" +
			"Ancient Tomb,drop,,10%,5,60d\n" +
			"Barrowgoyf,rise,,12.5,,2w\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if r := rows[0]; r.Op != "under" || r.Threshold != 5 || r.Pct != 0 {
		t.Errorf("absolute row = %+v", r)
	}
	if r := rows[1]; r.Op != "drop" || r.Pct != 0.10 || r.MinMove != 5 || r.WindowDays != 60 {
		t.Errorf("drop row = %+v", r)
	}

	if r := rows[2]; r.Op != "rise" || r.Pct != 0.125 || r.WindowDays != 14 {
		t.Errorf("rise row = %+v", r)
	}

	if rows[2].MinMove != store.DefaultMinMove {
		t.Errorf("min move = %v, want the default %v", rows[2].MinMove, store.DefaultMinMove)
	}
}

func TestJSONPercentIsAFraction(t *testing.T) {
	rows, err := Parse([]byte(
		`[{"name":"Ancient Tomb","direction":"drop","percent":0.1,"minMoveUsd":5,"sinceDays":60},
		  {"name":"Sol Ring","direction":"over","thresholdUsd":30}]`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r := rows[0]; r.Pct != 0.10 || r.MinMove != 5 || r.WindowDays != 60 || r.Threshold != 0 {
		t.Errorf("drop entry = %+v", r)
	}
	if r := rows[1]; r.Threshold != 30 || r.Pct != 0 {
		t.Errorf("absolute entry = %+v", r)
	}
}

func TestPercentUnitsAreExclusive(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"both cells", "Name,Direction,Threshold,Percent\nSol Ring,drop,5,10\n",
			"line 2 (Sol Ring): a row states a threshold or a percentage, not both"},
		{"dollars on a movement", "Name,Direction,Threshold,Percent\nSol Ring,drop,5,\n",
			"drop is a movement and takes no dollar threshold"},
		{"percent on a line", "Name,Direction,Threshold,Percent\nSol Ring,under,,10\n",
			"under is a dollar threshold and takes no percentage"},
		{"movement with no size", "Name,Direction,Threshold,Percent\nSol Ring,rise,,\n",
			"rise needs a percentage"},
		{"a fraction where a percentage goes", "Name,Direction,Percent\nSol Ring,drop,0.1\n",
			"a percentage, not a fraction"},
		{"a drop to nothing", "Name,Direction,Percent\nSol Ring,drop,100\n",
			"falling to nothing"},
		{"unparseable window", "Name,Direction,Percent,Since\nSol Ring,drop,10,soon\n",
			`cannot parse a window from "soon"`},
		{"unparseable floor", "Name,Direction,Percent,Min Move\nSol Ring,drop,10,lots\n",
			`cannot parse minimum move "lots"`},
		{"neither size column", "Name,Direction\nSol Ring,drop\n",
			`watch CSV needs a "Threshold" or "Percent" column`},
		{"json both", `[{"name":"Sol Ring","direction":"drop","thresholdUsd":5,"percent":0.1}]`,
			"entry 1 (Sol Ring): a row states a threshold or a percentage, not both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestOldWatchFilesStillParse(t *testing.T) {
	rows, err := Parse([]byte("Name,Direction,Threshold\nSol Ring,under,5\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 || rows[0].Threshold != 5 || rows[0].Pct != 0 {
		t.Fatalf("rows = %+v", rows)
	}

	if rows[0].MinMove != 0 || rows[0].WindowDays != 0 {
		t.Errorf("absolute row carried movement defaults: %+v", rows[0])
	}
}
