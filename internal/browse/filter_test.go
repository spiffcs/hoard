package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func TestParseFilterTerms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(*testing.T, filter)
	}{
		{"bare word is a name search", "sol", func(t *testing.T, f filter) {
			if len(f.names) != 1 || f.names[0] != "sol" {
				t.Errorf("names = %v", f.names)
			}
			if f.needsCatalog() {
				t.Error("a name search must not need the catalog")
			}
		}},
		{"several bare words all narrow", "sol ring", func(t *testing.T, f filter) {
			if len(f.names) != 2 {
				t.Errorf("names = %v, want both words", f.names)
			}
		}},
		{"quoted value keeps its spaces", `artist:"Seb McKinnon"`, func(t *testing.T, f filter) {
			if len(f.traits.Artists) != 1 || f.traits.Artists[0] != "Seb McKinnon" {
				t.Errorf("artists = %v", f.traits.Artists)
			}
		}},
		{"rarity is a trait", "rarity:mythic", func(t *testing.T, f filter) {
			if len(f.traits.Rarities) != 1 || !f.needsCatalog() {
				t.Errorf("traits = %+v", f.traits)
			}
		}},
		{"type has a short alias", "t:creature", func(t *testing.T, f filter) {
			if len(f.traits.Types) != 1 || f.traits.Types[0] != "creature" {
				t.Errorf("types = %v", f.traits.Types)
			}
		}},
		{"colour letters split", "color:WU", func(t *testing.T, f filter) {
			if len(f.traits.Colors) != 2 {
				t.Errorf("colors = %v, want two letters", f.traits.Colors)
			}
		}},
		{"cmc is a numeric trait", "cmc>=3", func(t *testing.T, f filter) {
			if len(f.traits.CMC) != 1 || f.traits.CMC[0].Op != ">=" || f.traits.CMC[0].Value != 3 {
				t.Errorf("cmc = %+v", f.traits.CMC)
			}
		}},
		{"qty is a holding term", "qty>2", func(t *testing.T, f filter) {
			if len(f.nums["qty"]) != 1 {
				t.Errorf("nums = %+v", f.nums)
			}
			if f.needsCatalog() {
				t.Error("a holding term must not need the catalog")
			}
		}},
		{"finish is a holding term", "finish:foil", func(t *testing.T, f filter) {
			if len(f.finishes) != 1 || f.finishes[0] != "foil" {
				t.Errorf("finishes = %v", f.finishes)
			}
		}},
		{"keys are case-insensitive", "RARITY:mythic", func(t *testing.T, f filter) {
			if len(f.traits.Rarities) != 1 {
				t.Errorf("traits = %+v", f.traits)
			}
		}},
		{"mixed halves", "rarity:mythic qty>1 sol", func(t *testing.T, f filter) {
			if len(f.traits.Rarities) != 1 || len(f.nums["qty"]) != 1 || len(f.names) != 1 {
				t.Errorf("filter = %+v", f)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parseFilter(tt.input)
			if err != nil {
				t.Fatalf("parseFilter(%q): %v", tt.input, err)
			}
			tt.check(t, f)
		})
	}
}

func TestParseFilterRejectsBadInput(t *testing.T) {
	for _, tt := range []struct{ input, wantIn string }{
		{"rarity:", "needs a value"},
		{"cmc>", "needs a value"},
		{"cmc>abc", "wants a number"},
		{"nonsense:x", "unknown key"},
		{"rarity>mythic", "cannot be compared"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseFilter(tt.input)
			if err == nil {
				t.Fatalf("parseFilter(%q) accepted it", tt.input)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantIn)
			}
		})
	}
}

func TestParseFilterToleratesAnUnclosedQuote(t *testing.T) {
	f, err := parseFilter(`artist:"Seb McKin`)
	if err != nil {
		t.Fatalf("parseFilter: %v", err)
	}
	if len(f.traits.Artists) != 1 || f.traits.Artists[0] != "Seb McKin" {
		t.Errorf("artists = %v", f.traits.Artists)
	}
}

func TestParseFilterKeepsHostileValuesIntact(t *testing.T) {
	f, err := parseFilter(`artist:"'; DROP TABLE cards--"`)
	if err != nil {
		t.Fatalf("parseFilter: %v", err)
	}
	if len(f.traits.Artists) != 1 || f.traits.Artists[0] != `'; DROP TABLE cards--` {
		t.Errorf("artists = %q", f.traits.Artists)
	}
}

func TestFilterMatches(t *testing.T) {
	foil := card{
		ScryfallID: "a", Name: "Ancient Tomb", SetCode: "uma",
		Finish: finish.Foil, Board: "main", Quantity: 1, Price: price(134), Value: 134,
	}
	normal := card{
		ScryfallID: "b", Name: "Sol Ring", SetCode: "c21",
		Finish: finish.Nonfoil, Board: "side", Quantity: 40, Price: price(1.10), Value: 44,
	}
	unpriced := card{ScryfallID: "c", Name: "Mystery", SetCode: "xyz", Finish: finish.Nonfoil}

	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"a", "b", "c"}},
		{"sol", []string{"b"}},
		{"SOL", []string{"b"}},
		{"set:uma", []string{"a"}},
		{"finish:foil", []string{"a"}},
		{"board:side", []string{"b"}},
		{"qty>10", []string{"b"}},
		{"qty>=1", []string{"a", "b"}},
		{"value>100", []string{"a"}},

		{"price<1", nil},
		{"price>0", []string{"a", "b"}},

		{"finish:nonfoil qty>10", []string{"b"}},
		{"finish:nonfoil qty>100", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			f, err := parseFilter(tt.query)
			if err != nil {
				t.Fatalf("parseFilter: %v", err)
			}
			var got []string
			for _, c := range []card{foil, normal, unpriced} {
				if f.matches(c, nil) {
					got = append(got, c.ScryfallID)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("matched %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterNilAllowedMeansNoOpinion(t *testing.T) {
	c := card{ScryfallID: "a", Name: "Sol Ring"}
	f, _ := parseFilter("sol")
	if !f.matches(c, nil) {
		t.Error("nil allowed set rejected a row")
	}
	if f.matches(c, map[string]bool{}) {
		t.Error("an empty allowed set should reject everything")
	}
	if !f.matches(c, map[string]bool{"a": true}) {
		t.Error("a matching allowed set rejected its own row")
	}
}

func TestNumCondComparisons(t *testing.T) {
	for _, tt := range []struct {
		op   string
		val  float64
		have float64
		want bool
	}{
		{">", 3, 4, true}, {">", 3, 3, false},
		{">=", 3, 3, true}, {"<", 3, 2, true},
		{"<=", 3, 3, true}, {"=", 3, 3, true}, {"=", 3, 4, false},
	} {
		if got := compare(tt.have, store.NumCond{Op: tt.op, Value: tt.val}); got != tt.want {
			t.Errorf("%v %s %v = %v, want %v", tt.have, tt.op, tt.val, got, tt.want)
		}
	}
}
