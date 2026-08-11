package command

import "github.com/spiffcs/hoard/internal/ui"

// unresolvedHeading is the sentence that introduces the cards an import could
// not name, shared by every command that can leave some behind: deck add, add,
// import, watch import, and the three browser operations that wrap them.
//
// It exists as a function rather than a format string because the sentence
// agrees with its count in two places, not one. "%d cards could not be
// resolved and were skipped:" is what all seven sites used to hold, and at
// n == 1 it printed "1 cards ... were skipped" — a noun helper alone would
// have fixed the noun and left the verb. Seven copies of one sentence is also
// seven places for the next edit to miss.
//
// This lands on the error path, which is the path read most carefully.
func unresolvedHeading(n int) string {
	return ui.Plural(n,
		"card could not be resolved and was skipped:",
		"cards could not be resolved and were skipped:")
}

// unreadableHeading is the companion for lines the parser never got as far as
// resolving. The two are separate sentences on purpose: a line that would not
// parse was never looked up against anything, so calling it unresolved would
// name a lookup that never happened.
func unreadableHeading(n int) string {
	return ui.Plural(n,
		"line could not be read and was skipped:",
		"lines could not be read and were skipped:")
}
