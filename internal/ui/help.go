package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func WrapHelp(s string, width int) []string {
	var lines []string
	for seg := range strings.SplitSeq(s, "\n") {
		lines = append(lines, wrapHelpSegment(seg, width)...)
	}
	return lines
}

func wrapHelpSegment(s string, width int) []string {
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

var HelpCommands = HelpEntry{Key: ":", Label: "commands"}

func Help(entries ...HelpEntry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if s := e.String(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

func K(key, label string) HelpEntry { return HelpEntry{Key: key, Label: label} }

func Say(label string) HelpEntry { return HelpEntry{Label: label} }
