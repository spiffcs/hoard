package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// WrapHelp breaks a help line between its " · " entries so every key stays
// visible on a narrow terminal and no hint is split mid-phrase — a hint that
// would be cut moves whole to the next line. A single entry longer than the
// width stands alone.
func WrapHelp(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	cur := ""
	for part := range strings.SplitSeq(s, " · ") {
		joined := part
		if cur != "" {
			joined = cur + " · " + part
		}
		if cur == "" || lipgloss.Width(joined) <= width {
			cur = joined
			continue
		}
		lines = append(lines, cur)
		cur = part
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// A help line is a list of entries joined by " · ", and it is worth having a
// type for because the alternative does not scale.
//
// These lines were string literals, one per view, and the same hint appeared
// in eleven of them. Changing what the palette key is called meant editing
// eleven strings and the tests that substring-matched them; adding an entry
// to the middle of one meant getting the separators right by hand. Neither
// is hard, both are the kind of edit that silently misses a case.
//
// As entries, a shared hint is a shared value — declared once, spelled the
// same everywhere by construction — and composing a line is appending to a
// slice rather than splicing a string.

// HelpEntry is one hint: the key that does it, and what it does. Key is
// empty for the entries that are instructions rather than keys ("type to
// narrow"), which read as prose and take no prefix.
type HelpEntry struct {
	Key   string
	Label string
}

func (e HelpEntry) String() string {
	if e.Key == "" {
		return e.Label
	}
	return e.Key + " " + e.Label
}

// HelpCommands is the command palette, spelled once for every view in every
// program that offers one. It leads its line: it is the only key that means
// the same thing everywhere, so it stops being read as a hint about this
// screen and starts being read as how the program works.
var HelpCommands = HelpEntry{Key: ":", Label: "commands"}

// Help joins entries into a line. Empty entries drop out, so a caller can
// build a line with a conditional slot in it without checking first.
func Help(entries ...HelpEntry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if s := e.String(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// K is a shorthand for a keyed entry, so a composed line reads as a list of
// hints rather than a list of struct literals.
func K(key, label string) HelpEntry { return HelpEntry{Key: key, Label: label} }

// Say is an entry with no key — an instruction rather than a shortcut.
func Say(label string) HelpEntry { return HelpEntry{Label: label} }
