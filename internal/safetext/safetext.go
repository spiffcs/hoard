// Package safetext strips the characters a terminal interprets from strings
// hoard did not write itself.
//
// A terminal is an interpreter, not a display. Bytes handed to it can move the
// cursor, repaint lines that have already scrolled past, retitle the window,
// and — with OSC 52, which iTerm2, kitty and WezTerm all implement — write the
// user's clipboard. A deck name is not markup, so nothing here is escaped or
// encoded; the characters are simply removed, because a name that contains
// them is a name nobody typed on purpose.
//
// WHY AT INGEST RATHER THAN AT RENDER. The rendering sites are many and grow:
// the browse TUI, `deck list`, the valuation report, CSV export, JSON export,
// and every error message that quotes a name back. Sanitising at each one is a
// list that will be incomplete the first time somebody adds a view. The places
// a foreign string ENTERS are few and change rarely — a deck fetched from a
// URL, a decklist read from a file, a CSV imported from another tracker — so
// that is where this is called. It also means what lands in SQLite is already
// clean, and an export cannot carry a payload back out to somebody else.
//
// This is not defence against a hostile local user: whoever runs hoard can
// already write to their own terminal. It is defence against a string that
// arrived from somewhere else — `hoard deck add https://archidekt.com/...`
// fetches a deck whose name is chosen by whoever built it, and a collection
// CSV is routinely passed between people.
//
// SINGLE CHARACTERS, NOT WHOLE SEQUENCES, AND THAT IS DELIBERATE.
// `Burn\x1b]52;c;cGF5bG9hZA==\x07` comes out as `Burn]52;c;cGF5bG9hZA==`, not
// as `Burn`: the ESC and the BEL go, the parameter bytes stay, because they
// are ordinary printable ASCII. The attack is dead either way — every one of
// those characters is inert without the ESC that gives them meaning.
//
// Removing the whole sequence would mean parsing CSI and OSC grammars here,
// and a parser is a thing that can be wrong. It could also over-delete: `]0;`
// is legal text in a deck name. So this removes exactly the characters with
// power and leaves the rest, which has the useful side effect that a tampered
// name still LOOKS tampered rather than being quietly rewritten into something
// that reads as legitimate.
package safetext

import (
	"strings"
	"unicode/utf8"
)

// MaxNameRunes bounds a name at a length no human chose.
//
// The limit is on runes rather than bytes so a name in a non-Latin script is
// not cut shorter than a Latin one saying the same thing. It exists because
// nothing else bounds these strings: a deck fetched from a URL can name itself
// with a megabyte, and the cost lands on every render, every export and every
// row of a table that has to measure it.
const MaxNameRunes = 256

// Clean returns s with everything a terminal would act on removed, invalid
// UTF-8 replaced, surrounding whitespace trimmed, and the result bounded to
// MaxNameRunes.
//
// A string that was already clean is returned unchanged and unallocated, which
// is the overwhelmingly common case — the fast path exists so this can be
// called at every boundary without anybody weighing whether it is worth it.
func Clean(s string) string {
	if ok(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	n := 0
	for _, r := range s {
		if n >= MaxNameRunes {
			break
		}
		// RuneError from a range over a string means an invalid byte. It is
		// replaced rather than dropped so a mangled encoding is visible as
		// mojibake instead of silently becoming a different, plausible name.
		if r == utf8.RuneError {
			b.WriteRune('�')
			n++
			continue
		}
		if interpreted(r) {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// interpreted reports whether a terminal or a text renderer would treat r as
// an instruction rather than as a character.
func interpreted(r rune) bool {
	switch {
	// C0 controls and DEL. ESC (0x1B) is the one that starts CSI and OSC
	// sequences, but the whole block goes: a bare CR repaints the current line,
	// a BEL is audible, and NUL truncates the string for anything downstream
	// that hands it to C. Tab and newline are included deliberately — these are
	// single-line names, and a name containing a newline can forge a row in
	// every table hoard prints.
	case r < 0x20, r == 0x7F:
		return true
	// C1 controls. In UTF-8 these are two bytes and most terminals ignore them,
	// but a terminal decoding the same bytes as Latin-1 reads U+009B as CSI —
	// the eight-bit form of ESC[. Cheap to remove, and it closes the variant of
	// this attack that survives an encoding mismatch.
	case r >= 0x80 && r <= 0x9F:
		return true
	// Bidirectional overrides and isolates: the Trojan Source characters
	// (CVE-2021-42574). They reorder the *display* of text without changing its
	// bytes, so a deck can be shown under one name and stored under another —
	// which defeats the reader's ability to check what they are looking at.
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// ok reports whether Clean would return s unchanged, without allocating.
func ok(s string) bool {
	if len(s) > MaxNameRunes*utf8.UTFMax {
		return false
	}
	n := 0
	for i, r := range s {
		// A RuneError at width 1 is a genuine decode failure; the real U+FFFD
		// is three bytes wide and is left alone.
		if r == utf8.RuneError {
			if _, w := utf8.DecodeRuneInString(s[i:]); w == 1 {
				return false
			}
		}
		if interpreted(r) {
			return false
		}
		n++
	}
	return n <= MaxNameRunes && strings.TrimSpace(s) == s
}
