package safetext

import (
	"strings"
	"unicode/utf8"
)

const MaxNameRunes = 256

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

func interpreted(r rune) bool {
	switch {

	case r < 0x20, r == 0x7F:
		return true

	case r >= 0x80 && r <= 0x9F:
		return true

	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

func ok(s string) bool {
	if len(s) > MaxNameRunes*utf8.UTFMax {
		return false
	}
	n := 0
	for i, r := range s {

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
