package decksource

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func FuzzParseLine(f *testing.F) {
	f.Add("2 Sol Ring")
	f.Add("1x Bolt (2X2) 117 *F*")
	f.Add("SB: 1 Force of Will")
	f.Add("4 Lightning Bolt (LEA) 161 *E*")
	f.Add("0 Zero Quantity")
	f.Add("999999999999999999999 Overflow")
	f.Add("1 Card () ")
	f.Add("1 Card (SET) *f*")
	f.Add("")
	f.Add("// a comment")

	f.Fuzz(func(t *testing.T, line string) {
		e, ok := parseLine(line)
		if !ok {
			return
		}

		if e.Quantity < 1 {
			t.Fatalf("quantity %d from %q", e.Quantity, line)
		}

		switch e.Finish {
		case finish.Nonfoil, finish.Foil, finish.Etched:
		default:
			t.Fatalf("finish %q from %q", e.Finish, line)
		}

		if e.Board != "" && e.Board != BoardSide {
			t.Fatalf("board %q from %q", e.Board, line)
		}

		hasPair := e.Ident.Set != "" && e.Ident.CollectorNumber != ""
		hasName := e.Ident.Name != ""
		if !hasPair && !hasName {
			t.Fatalf("entry identifies nothing, from %q", line)
		}
		if e.Ident.Set != "" && e.Ident.CollectorNumber == "" && !hasName {
			t.Fatalf("set %q with neither a collector number nor a name, from %q", e.Ident.Set, line)
		}

		if e.Ident.Set != strings.ToLower(e.Ident.Set) {
			t.Fatalf("set %q is not lowercased, from %q", e.Ident.Set, line)
		}
	})
}
