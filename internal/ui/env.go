// Package ui renders the CLI's tables. It exists so that hoard's four table
// commands share one layout engine, one money format, and one set of styles,
// instead of each hand-rolling a text/tabwriter grid.
//
// The package deliberately takes its terminal description as a value (Env)
// rather than reaching for os.Stdout inside render paths, so every layout
// decision is reproducible in a test at any terminal width.
package ui

import (
	"os"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// defaultWidth is assumed whenever the real width can't be discovered.
const defaultWidth = 80

// Env describes everything about the output device that changes rendering.
// It is a value, never consulted globally inside render paths, so tests can
// pin any terminal shape without touching os.Stdout.
type Env struct {
	Width int  // usable columns; the table is laid out to fit exactly
	Color bool // ANSI styling permitted
	Clamp bool // columns may be truncated to fit Width
	Bars  bool // share bars are worth drawing
}

// barsMinWidth is the narrowest terminal that still gets share bars; below it
// the bar column degrades to a percentage (see FitColumns).
const barsMinWidth = 50

// Detect describes the terminal behind f.
//
// Resolution order is HOARD_WIDTH, then the real terminal size, then 80. A
// non-terminal (a pipe, a file, a test) gets full-fidelity output: no styling,
// no truncation and no bars, so `hoard summary | grep` sees whole deck names
// and zero escape sequences.
func Detect(f *os.File) Env {
	if v, ok := os.LookupEnv("HOARD_WIDTH"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			// 0 means "unlimited": render at natural width, truncating nothing.
			if n == 0 {
				return Env{Width: 0, Color: colorOK(f), Clamp: false, Bars: true}
			}
			return Env{Width: n, Color: colorOK(f), Clamp: true, Bars: n >= barsMinWidth}
		}
	}

	if !term.IsTerminal(f.Fd()) {
		return Env{Width: defaultWidth, Color: false, Clamp: false, Bars: false}
	}

	w := terminalWidth(f)
	return Env{Width: w, Color: colorOK(f), Clamp: true, Bars: w >= barsMinWidth}
}

// terminalWidth asks the terminal, then $COLUMNS, then gives up and assumes 80.
func terminalWidth(f *os.File) int {
	if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
		return w
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return defaultWidth
}

// colorOK reports whether ANSI styling should be emitted. lipgloss's profile
// detection already accounts for NO_COLOR, CLICOLOR and non-terminal output.
func colorOK(f *os.File) bool {
	if !term.IsTerminal(f.Fd()) {
		return false
	}
	return lipgloss.ColorProfile() != termenv.Ascii
}

// IsTerminal reports whether f is a real terminal. The one TTY test every
// caller should share, so "is this interactive?" is answered the same way
// the renderer answers it.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

// Style renders a fragment of text. Resolving styles to a function (rather than
// consulting lipgloss inside the renderer) keeps Env.Color authoritative and
// keeps tests independent of lipgloss's process-wide, once-cached color profile.
type Style func(string) string

func plain(s string) string { return s }

// The full palette lives in theme.go — one definition serving both these
// Env methods and the bubbletea Theme. Bold and Dim remain the workhorses:
// structure comes from weight and alignment, color only ever carries
// meaning (identity, semantic state), never decoration.

// Bold styles primary text: section headers and totals.
func (e Env) Bold() Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return lgBold.Render(s) }
}

// Dim styles secondary text: column titles, source/finish columns, bars, hints.
func (e Env) Dim() Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return lgFaint.Render(s) }
}
