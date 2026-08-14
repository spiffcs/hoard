package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

			b.WriteString(open)
		}
		i += len(seq)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func escapeAt(s string) (seq string, isSGR bool) {
	if len(s) < 2 || s[1] != '[' {

		return s[:1], false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]

		if (c >= '0' && c <= '9') || c == ';' || c == ':' || c == ' ' {
			continue
		}
		return s[:i+1], c == 'm'
	}
	return s, false
}

func sgrOf(style lipgloss.Style) (string, bool) {
	const marker = "\x00"
	rendered := style.Render(marker)
	i := strings.Index(rendered, marker)
	if i <= 0 {
		return "", false
	}
	return rendered[:i], true
}
