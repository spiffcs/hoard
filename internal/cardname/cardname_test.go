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
		// Text that merely contains a short name is not that card. This is the
		// case the whole check exists for: Scryfall's fuzzy endpoint returns
		// "Opt" for all three.
		{"option", "Opt", false, "containing a short name is not being it"},
		{"options", "Opt", false, "same, pluralized"},
		{"adopt", "Opt", false, "short name embedded mid-word"},

		{"Opt", "Opt", true, "exact read of a short name"},
		{"opt.", "Opt", true, "punctuation is not a difference"},
		{"Fog", "Fog", true, "exact read of another short name"},

		{"Elspeth, Knight-Errant", "Elspeth, Knight-Errant", true, "clean read"},
		{"Elspeth Knight Errant", "Elspeth, Knight-Errant", true, "punctuation dropped"},
		{"Elspeth", "Elspeth, Knight-Errant", true, "partial read of a longer name"},

		// The substitutions OCR actually makes.
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

// The copyright line is printed on every card, so OCR reads it often. It scores
// 0.71 similarity against the real card "Wizards of Thay" — over the bar, and
// close enough to it that no threshold on similarity alone separates the two.
// The length guard is what rejects it: a fifth of the text is left unexplained.
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
	// But the guard must not fire on a partial read, which is legitimately much
	// shorter than its name and is handled by the prefix rule before it.
	if !Plausible("Bitter", "Bitterblossom") {
		t.Error("the length guard rejected a partial read")
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
	// A name shorter than a trigram yields itself, so it is still findable
	// rather than silently absent from the index.
	if got := Trigrams("ai"); !slices.Equal(got, []string{"ai"}) {
		t.Errorf("Trigrams(short) = %v", got)
	}
	if got := Trigrams(""); got != nil {
		t.Errorf("Trigrams(empty) = %v, want nil", got)
	}
	// Repeats are collapsed: a gram appearing twice would double that name's
	// score in the candidate ranking for no reason.
	if got := Trigrams("aaaa"); !slices.Equal(got, []string{"aaa"}) {
		t.Errorf("Trigrams with repeats = %v, want them deduplicated", got)
	}
}
