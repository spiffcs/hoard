package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/ui"
)

// One line per row: an accent bar on the selection, pips for a printing
// with a known identity, nothing (not a dash) for an unknown one, dim
// description text appended.
func TestCascadeDelegateRendersOneLine(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	l := list.New([]list.Item{
		printItem{card: scryfall.Card{Set: "uma", CollectorNumber: "7",
			SetName: "Ultimate Masters", ColorIdentity: []string{"W", "U"}}},
		printItem{card: scryfall.Card{Set: "c21", CollectorNumber: "1",
			SetName: "Commander 2021"}},
	}, cascadeDelegate{theme: ui.DefaultTheme()}, 60, 8)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)

	view := l.View()
	if strings.Count(view, "▌") != 1 {
		t.Errorf("want exactly one selection marker:\n%q", view)
	}
	var withPips, without string
	for line := range strings.SplitSeq(view, "\n") {
		switch {
		case strings.Contains(line, "UMA"):
			withPips = line
		case strings.Contains(line, "C21"):
			without = line
		}
	}
	if withPips == "" || without == "" {
		t.Fatalf("rows missing from view:\n%q", view)
	}
	// Each pip letter carries its own color, so W and U are separately
	// wrapped rather than contiguous.
	if !strings.Contains(withPips, "mW\x1b[0m") || !strings.Contains(withPips, "mU\x1b[0m") ||
		!strings.Contains(withPips, "38;2;") {
		t.Errorf("identity row lost its colored pips: %q", withPips)
	}
	// Only the title segment matters here — the dim description may carry
	// its own em dashes ("$— / $—f" for an unpriced card).
	title := without
	if i := strings.Index(without, "\x1b[2m"); i >= 0 {
		title = without[:i]
	}
	if strings.Contains(title, "—") {
		t.Errorf("unknown identity must show nothing, not the dash: %q", without)
	}
}
