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
