package decksource

import (
	"strings"
	"testing"
)

// parseLine reads decklists people paste in from anywhere — Moxfield, Archidekt,
// a text file edited by hand, the output of another tool's exporter. Every line
// is untrusted input, and a line that parses into a *wrong* Entry is worse than
// one that fails: it silently adds the wrong card to someone's collection.
//
//	go test ./internal/decksource/ -run Fuzz -fuzz FuzzParseLine -fuzztime 30s

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

		// A quantity below one is clamped, never passed through: a "0 Sol Ring"
		// line that survived as quantity 0 would add a row nothing owns.
		if e.Quantity < 1 {
			t.Fatalf("quantity %d from %q", e.Quantity, line)
		}

		// Finish is a closed vocabulary the store and the JSON schema both
		// enforce; anything else fails much later, at a write.
		switch e.Finish {
		case "nonfoil", "foil", "etched":
		default:
			t.Fatalf("finish %q from %q", e.Finish, line)
		}

		// Board is either unset (the caller fills in the section it is
		// reading) or the one value an "SB:" prefix can produce.
		if e.Board != "" && e.Board != BoardSide {
			t.Fatalf("board %q from %q", e.Board, line)
		}

		// The identifier is either a set+number pair or a name, never a
		// half-filled mixture: a resolve given a set with no number searches
		// the whole set and picks arbitrarily.
		hasPair := e.Ident.Set != "" && e.Ident.CollectorNumber != ""
		hasName := e.Ident.Name != ""
		if !hasPair && !hasName {
			t.Fatalf("entry identifies nothing, from %q", line)
		}
		if e.Ident.Set != "" && e.Ident.CollectorNumber == "" {
			t.Fatalf("set %q with no collector number, from %q", e.Ident.Set, line)
		}

		// Set codes are lowercased at the boundary so a later comparison
		// never has to guess the case.
		if e.Ident.Set != strings.ToLower(e.Ident.Set) {
			t.Fatalf("set %q is not lowercased, from %q", e.Ident.Set, line)
		}
	})
}
