package ui

import "fmt"

// Plural writes "1 card" but "2 cards".
//
// Both forms are spelled out rather than derived by appending an "s", because
// the nouns hoard counts are not all regular — a collection has watches, not
// watchs — and because the caller is often agreeing a verb as well as a noun:
// Plural(n, "line was", "lines were") is the intended shape, not an abuse of
// one. English puts number agreement in more than one place per sentence, and
// a helper that only handles the noun leaves "1 card ... were skipped" behind.
//
// The count keeps a bare %d rather than going through Count: these strings are
// prose, and prose in hoard counts without thousands separators ("Added 1200
// cards into Binder"), while Count's grouping belongs to table columns.
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// PluralCount is Plural for a sentence that sits with a table rather than in
// running prose — a total under a column of figures, where a bare 12847 would
// be the only ungrouped number on screen. The singular case needs no grouping
// by definition, so the two agree at n == 1.
func PluralCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return Count(n) + " " + plural
}
