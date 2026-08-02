package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The whole point: an inner reset must not switch the outer style off, and
// the inner styling must survive rather than being stripped.
func TestRestyle(t *testing.T) {
	reverse := lipgloss.NewStyle().Reverse(true)
	faint := lipgloss.NewStyle().Faint(true)
	tint := lipgloss.NewStyle().Foreground(lipgloss.Color("#b5cde3"))

	t.Run("plain line gains the style", func(t *testing.T) {
		got := Restyle("Sol Ring", reverse)
		if !strings.HasPrefix(got, "\x1b[7m") || !strings.HasSuffix(got, "\x1b[0m") {
			t.Errorf("Restyle = %q, want it wrapped in reverse", got)
		}
		if ansi.Strip(got) != "Sol Ring" {
			t.Errorf("text changed: %q", ansi.Strip(got))
		}
	})

	t.Run("inner reset cannot end the bar", func(t *testing.T) {
		line := "Name " + faint.Render("dim cell") + " after"
		got := Restyle(line, reverse)
		// After the dim cell's closing reset, the reverse must be
		// re-asserted before "after" renders.
		i := strings.LastIndex(got, "after")
		if i < 0 {
			t.Fatalf("text lost: %q", got)
		}
		if !strings.Contains(got[:i], "\x1b[0m\x1b[7m") {
			t.Errorf("no re-assertion after the inner reset: %q", got)
		}
	})

	t.Run("inner tint survives", func(t *testing.T) {
		line := tint.Render("Island") + " rest"
		got := Restyle(line, reverse)
		if !strings.Contains(got, "38;2;") {
			t.Errorf("the identity tint was stripped: %q", got)
		}
		if !strings.HasPrefix(got, "\x1b[7m") {
			t.Errorf("outer style missing: %q", got)
		}
	})

	t.Run("width is preserved", func(t *testing.T) {
		for _, line := range []string{
			"",
			"plain",
			faint.Render("a") + "b" + tint.Render("c"),
			strings.Repeat("x", 40),
		} {
			got := Restyle(line, reverse)
			if ansi.StringWidth(got) != ansi.StringWidth(line) {
				t.Errorf("width changed for %q: %d != %d",
					line, ansi.StringWidth(got), ansi.StringWidth(line))
			}
		}
	})

	t.Run("empty style is a no-op", func(t *testing.T) {
		line := faint.Render("dim") + " x"
		if got := Restyle(line, lipgloss.NewStyle()); got != line {
			t.Errorf("empty style rewrote the line: %q", got)
		}
	})

	t.Run("non-SGR escapes pass through without re-assertion", func(t *testing.T) {
		line := "a\x1b[2Kb" // erase-line: CSI ending in K, not m
		got := Restyle(line, reverse)
		if !strings.Contains(got, "\x1b[2K") {
			t.Errorf("non-SGR CSI mangled: %q", got)
		}
		if strings.Contains(got, "\x1b[2K\x1b[7m") {
			t.Errorf("re-asserted after a non-SGR sequence: %q", got)
		}
	})

	t.Run("bare trailing escape survives", func(t *testing.T) {
		got := Restyle("x\x1b", reverse)
		if ansi.Strip(got) != "x" {
			t.Errorf("trailing ESC corrupted the text: %q", got)
		}
	})
}
