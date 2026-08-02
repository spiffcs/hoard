package ui

// Restyle: overlaying one style onto an already-styled line without erasing
// the styling it carries. The selection bar was the motivating case — the
// old approach stripped the row's escapes before applying reverse video,
// because an embedded reset would have switched the reverse off mid-line;
// stripping also threw away the identity tints the row was carrying.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Restyle renders line under style without discarding the line's own SGR
// styling: the style's sequence opens the line and is re-asserted after
// every embedded SGR sequence, so an inner reset (or any inner style) can
// never switch the outer style off. A reverse-video bar over identity-
// tinted cells keeps both — the bar spans the row, the tints show through.
//
// Only SGR (CSI …m) sequences are re-asserted after; other escape
// sequences pass through untouched. When style resolves to no escapes (an
// Ascii profile, an empty style) the line is returned unchanged.
func Restyle(line string, style lipgloss.Style) string {
	open, ok := sgrOf(style)
	if !ok {
		return line
	}
	var b strings.Builder
	b.WriteString(open)
	for i := 0; i < len(line); {
		if line[i] != 0x1b {
			b.WriteByte(line[i])
			i++
			continue
		}
		seq, isSGR := escapeAt(line[i:])
		b.WriteString(seq)
		if isSGR {
			// Whatever the inner sequence set or reset, the outer style
			// still holds; re-asserting unconditionally is idempotent.
			b.WriteString(open)
		}
		i += len(seq)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// escapeAt returns the escape sequence starting at s[0] (which must be
// ESC) and whether it is an SGR. An unterminated sequence is returned as
// what remains, closing nothing.
func escapeAt(s string) (seq string, isSGR bool) {
	if len(s) < 2 || s[1] != '[' {
		// Not a CSI (a bare ESC, or another introducer this renderer never
		// emits): pass the ESC through alone rather than guessing a length.
		return s[:1], false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		// CSI parameter and intermediate bytes; anything else is final.
		if (c >= '0' && c <= '9') || c == ';' || c == ':' || c == ' ' {
			continue
		}
		return s[:i+1], c == 'm'
	}
	return s, false
}

// sgrOf extracts the opening SGR sequence a style renders with, by
// rendering a marker and taking what precedes it. False when the style
// resolves to nothing — a no-color profile, an empty style.
func sgrOf(style lipgloss.Style) (string, bool) {
	const marker = "\x00"
	rendered := style.Render(marker)
	i := strings.Index(rendered, marker)
	if i <= 0 {
		return "", false
	}
	return rendered[:i], true
}
