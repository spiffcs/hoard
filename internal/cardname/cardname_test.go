package cardname

import (
	"slices"
	"testing"
)

func TestPlausible(t *testing.T) {
	cases := []struct {
		text, canonical string
		want            bool
		why             string
	}{

		{"option", "Opt", false, "containing a short name is not being it"},
		{"options", "Opt", false, "same, pluralized"},
		{"adopt", "Opt", false, "short name embedded mid-word"},

		{"Opt", "Opt", true, "exact read of a short name"},
		{"opt.", "Opt", true, "punctuation is not a difference"},
		{"Fog", "Fog", true, "exact read of another short name"},

		{"Elspeth, Knight-Errant", "Elspeth, Knight-Errant", true, "clean read"},
		{"Elspeth Knight Errant", "Elspeth, Knight-Errant", true, "punctuation dropped"},

		{"Elspeth", "Elspeth, Knight-Errant", false, "partial read must not resolve by prefix"},
		{"Gliding", "Gliding Licid", false, "a slide fragment must not become another card"},

		{"Sol Rlng", "Sol Ring", true, "l for i"},
		{"S0l Ring", "Sol Ring", true, "zero for o"},
		{"Anclent Tornb", "Ancient Tomb", true, "rn for m, and l for i"},
		{"Bitterbiossom", "Bitterblossom", true, "i for l"},

		{"control have indestructible", "Opt", false, "rules text"},
		{"Volkan Baga", "Elspeth, Knight-Errant", false, "artist line"},
	}
	for _, c := range cases {
		if got := Plausible(c.text, c.canonical); got != c.want {
			t.Errorf("Plausible(%q, %q) = %v, want %v — %s",
				c.text, c.canonical, got, c.want, c.why)
		}
	}
}

func TestPlausibleRejectsNearMissesThatDropWords(t *testing.T) {
	for _, c := range []struct{ text, canonical string }{
		{"Wizards of the Coast", "Wizards of Thay"},
		{"TM & (C) 2024 Wizards of the Coast", "Wizards of Thay"},
	} {
		if Plausible(c.text, c.canonical) {
			t.Errorf("Plausible(%q, %q) = true; a whole word is unaccounted for",
				c.text, c.canonical)
		}
	}

	if Plausible("Bitter", "Bitterblossom") {
		t.Error("a bare prefix fragment resolved to a name it cannot confirm")
	}
}

func TestNormalize(t *testing.T) {
	for _, c := range [][2]string{
		{"Elspeth, Knight-Errant", "elspethknighterrant"},
		{"Opt.", "opt"},
		{"  Sol   Ring  ", "solring"},
		{"Jötun Grunt", "jötungrunt"},
		{"", ""},
		{"---", ""},
	} {
		if got := Normalize(c[0]); got != c[1] {
			t.Errorf("Normalize(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestSimilarityAndEditDistance(t *testing.T) {
	if got := EditDistance("kitten", "sitting"); got != 3 {
		t.Errorf("EditDistance = %d, want 3", got)
	}
	if got := Similarity("", ""); got != 1 {
		t.Errorf("Similarity of two empties = %v, want 1", got)
	}
	if got := Similarity("abc", "abc"); got != 1 {
		t.Errorf("Similarity of equals = %v, want 1", got)
	}
	if Similarity("abc", "xyz") != 0 {
		t.Errorf("Similarity of disjoint = %v, want 0", Similarity("abc", "xyz"))
	}
}

func TestTrigrams(t *testing.T) {
	got := Trigrams("solring")
	want := []string{"sol", "olr", "lri", "rin", "ing"}
	if !slices.Equal(got, want) {
		t.Errorf("Trigrams = %v, want %v", got, want)
	}

	if got := Trigrams("ai"); !slices.Equal(got, []string{"ai"}) {
		t.Errorf("Trigrams(short) = %v", got)
	}
	if got := Trigrams(""); got != nil {
		t.Errorf("Trigrams(empty) = %v, want nil", got)
	}

	if got := Trigrams("aaaa"); !slices.Equal(got, []string{"aaa"}) {
		t.Errorf("Trigrams with repeats = %v, want them deduplicated", got)
	}
}
