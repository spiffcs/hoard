// Package cardname matches noisy text against card names.
//
// It exists apart from any UI or data source because two very different callers
// need the same notion of "is this the same card": the camera scanner, checking
// whether Scryfall's fuzzy answer actually explains what the OCR read, and the
// local catalog, ranking its own candidates for the same question. One
// definition means the two cannot disagree about what a match is.
package cardname

import (
	"strings"
	"unicode"
)

const (
	// MinFuzzyLen is the shortest text allowed to match a *different* name.
	// Exact matches bypass it, so genuinely short cards ("Opt", "Fog") still
	// scan.
	MinFuzzyLen = 4
	// MinSimilarity is how much of the text the matched name must account for,
	// as 1 - editDistance/length. Short names are only a few edits from plenty
	// of ordinary words ("adopt" is 2 from "opt"), so this has to sit above the
	// ratio those produce.
	MinSimilarity = 0.7
	// MaxLengthRatio is how much shorter or longer a corrupted read may be than
	// the name it matches, as a fraction of the longer.
	//
	// OCR substitutes characters — l for 1, o for 0, rn for m — and rarely drops
	// or invents whole words, so a genuine misreading is close to its name in
	// length as well as in content. Measured across real corruptions the two
	// classes separate here and nowhere else: "Anclent Tornb" differs from
	// "Ancient Tomb" by 0.08, while the copyright line "Wizards of the Coast"
	// sits 0.24 from the real card "Wizards of Thay" and scores a similarity of
	// 0.71 — over the bar above, and close enough to it that no threshold on
	// similarity alone separates them.
	MaxLengthRatio = 0.15
	// AutoCommitSimilarity is the bar a fuzzy match must clear before a scanned
	// card may be written to the collection without a human confirming it.
	// Deliberately far above MinSimilarity: 0.7 means "worth showing a person",
	// this means "right often enough to act on silently".
	//
	// 0.88 rather than the original 0.90, for old cards. Pre-1998 titles are
	// small serif type and lose a glyph or two to it — the corpus is full of
	// "Amrou Kichkin" for Amrou Kithkin and "Sisters of che Flame" for Sisters
	// of the Flame — and a two-character slip on a short name lands just under
	// 0.90. Prodigal Sorcerer scored exactly 0.88 live, on the right card, with
	// its year and border already agreeing, and queued anyway.
	//
	// The room to move came from where the false matches actually sit. Every
	// one on record is far below: "Stemal Dragon" at 0.76, a glare-truncated
	// title at 0.79, and the copyright line resolving to "Wizards of Thay" at
	// 0.71 — that last one the reason MaxLengthRatio exists, because no
	// similarity threshold separates it. Nothing observed lives between 0.88
	// and 0.90, so this widens the pass without reaching a known miss.
	//
	// It is still the *uncorroborated* bar. A card whose number and set agree
	// never consults this at all.
	AutoCommitSimilarity = 0.88
)

// Match describes how a fuzzy resolution earned its answer, so callers can
// distinguish a certain hit from a plausible-but-shaky one. Exact means the
// normalized text equalled the normalized canonical name; Similarity is 1 for
// an exact hit and the edit-distance score otherwise.
type Match struct {
	Exact      bool
	Similarity float64
}

// Plausible reports whether canonical is a believable reading of some text.
//
// Scryfall's fuzzy endpoint is a *search*, not an identity check: it resolves
// "option" to the card "Opt" because the query contains the name. That's right
// for a human typing a partial name and wrong for OCR, where any stray word in
// frame — a keycap, a sleeve, rules text — silently becomes a card. Short names
// are magnets for this, and trying several lines per capture gives noise several
// chances to hit one. So the match has to actually explain what was read.
func Plausible(text, canonical string) bool {
	o, c := Normalize(text), Normalize(canonical)
	if o == "" || c == "" {
		return false
	}
	if o == c {
		return true
	}
	if len(o) < MinFuzzyLen {
		return false
	}
	// A partial read of a longer name is fine ("elspeth" → "Elspeth,
	// Knight-Errant"). The reverse is the bug: text that merely *contains* a
	// short name must not resolve to it.
	//
	// This is checked before the length guard below, and has to be: a partial
	// read is legitimately much shorter than its name, which is the one case
	// where a large length difference means nothing is wrong.
	if strings.HasPrefix(c, o) {
		return true
	}
	// The mirror case: OCR glues the title to the line below it ("Inspired
	// Fire deals + tam", observed live), so the read *starts with* the whole
	// name. Anchored at the start and floored on length, this stays clear of
	// the containment bug — "option" starts with "opt", but a three-letter
	// name never clears the floor.
	if len(c) >= 8 && strings.HasPrefix(o, c) {
		return true
	}
	if lengthRatio(o, c) > MaxLengthRatio {
		return false
	}
	return Similarity(o, c) >= MinSimilarity
}

// lengthRatio is how far apart two strings are in length, as a fraction of the
// longer one.
func lengthRatio(a, b string) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 0
	}
	return float64(max(len(a), len(b))-min(len(a), len(b))) / float64(longest)
}

// Normalize reduces a name to comparable letters and digits, so punctuation and
// spacing differences between OCR and a catalog don't matter.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Similarity scores two normalized strings from 0 to 1 by edit distance.
func Similarity(a, b string) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 1
	}
	return 1 - float64(EditDistance(a, b))/float64(longest)
}

// EditDistance is the Levenshtein distance between two strings, computed with a
// single rolling row.
func EditDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// TrigramSize is the n in n-gram. Three is the usual compromise: shorter grams
// match too much to narrow anything, longer ones stop tolerating the
// single-character substitutions OCR actually makes.
const TrigramSize = 3

// Trigrams splits a normalized name into overlapping three-character grams, for
// the index that generates fuzzy candidates.
//
// A name shorter than three characters yields itself, so "ai" is still findable
// rather than silently absent from the index.
func Trigrams(normalized string) []string {
	r := []rune(normalized)
	if len(r) == 0 {
		return nil
	}
	if len(r) < TrigramSize {
		return []string{normalized}
	}
	seen := make(map[string]bool, len(r))
	out := make([]string, 0, len(r))
	for i := 0; i+TrigramSize <= len(r); i++ {
		g := string(r[i : i+TrigramSize])
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}
