package cardname

import (
	"strings"
	"unicode"
)

const (
	MinFuzzyLen = 4

	MinSimilarity = 0.7

	MaxLengthRatio = 0.15

	AutoCommitSimilarity = 0.88
)

type Match struct {
	Exact      bool
	Similarity float64

	PrefixOnly bool
}

func PrefixCandidate(text, canonical string) bool {
	o, c := Normalize(text), Normalize(canonical)
	if len(o) < MinFuzzyLen {
		return false
	}
	return len(c) > len(o) && strings.HasPrefix(c, o)
}

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

	if len(c) >= 8 && strings.HasPrefix(o, c) {
		return true
	}
	if lengthRatio(o, c) > MaxLengthRatio {
		return false
	}
	return Similarity(o, c) >= MinSimilarity
}

func lengthRatio(a, b string) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 0
	}
	return float64(max(len(a), len(b))-min(len(a), len(b))) / float64(longest)
}

func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Similarity(a, b string) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 1
	}
	return 1 - float64(EditDistance(a, b))/float64(longest)
}

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

const TrigramSize = 3

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
