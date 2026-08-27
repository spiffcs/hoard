package ui

import "github.com/charmbracelet/x/ansi"

func Width(s string) int {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] >= 0x7f {
			return ansi.StringWidth(s)
		}
	}
	return len(s)
}
