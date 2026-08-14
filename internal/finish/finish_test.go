package finish

import (
	"encoding/json"
	"testing"
)

func TestParseAcceptsOnlyTheThreeFinishes(t *testing.T) {
	all := All()
	if len(all) < 3 {
		t.Fatalf("All() = %v, want at least nonfoil, foil and etched", all)
	}
	seen := map[string]bool{}
	for _, want := range all {
		spelling := want.String()
		if spelling == "" {
			t.Errorf("finish %#v has an empty spelling", want)
			continue
		}
		if seen[spelling] {
			t.Errorf("All() lists %q twice", spelling)
		}
		seen[spelling] = true

		got, err := Parse(spelling)
		if err != nil {
			t.Errorf("Parse(%q): %v", spelling, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", spelling, got, want)
		}
	}
	for _, spelling := range []string{"nonfoil", "foil", "etched"} {
		if !seen[spelling] {
			t.Errorf("All() = %v, missing %q", all, spelling)
		}
	}

	for _, in := range []string{"normal", "", "shiny", "Foil", "foil ", "nonfoil\x00", "true"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %q with no error, want a rejection", in, got)
		}
	}
}

func TestFoilAndEtchedUseFoilPricing(t *testing.T) {
	for finish, want := range map[Finish]bool{Nonfoil: false, Foil: true, Etched: true} {
		if got := finish.UsesFoilPricing(); got != want {
			t.Errorf("%q.UsesFoilPricing() = %v, want %v", finish, got, want)
		}
	}
}

func TestFinishIsJSONText(t *testing.T) {
	all := All()
	if len(all) < 3 {
		t.Fatalf("All() = %v, want at least nonfoil, foil and etched", all)
	}
	for _, want := range all {
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", want, err)
		}
		if string(b) != `"`+want.String()+`"` {
			t.Errorf("Marshal(%q) = %s, want a plain JSON string", want, b)
		}

		var got Finish
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if got != want {
			t.Errorf("Unmarshal(%s) = %q, want %q", b, got, want)
		}
	}

	for _, in := range []string{`"normal"`, `"shiny"`, `""`, `7`, `null`, `{}`} {
		var got Finish
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Errorf("Unmarshal(%s) = %q with no error, want a rejection", in, got)
		}
	}
}
