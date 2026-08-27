package ui

import (
	"os"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

const defaultWidth = 80

type Env struct {
	Width int
	Color bool
	Clamp bool
	Bars  bool
}

const barsMinWidth = 50

func Detect(f *os.File) Env {
	if v, ok := os.LookupEnv("HOARD_WIDTH"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {

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

func terminalWidth(f *os.File) int {
	if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
		return w
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return defaultWidth
}

func colorOK(f *os.File) bool {
	if !term.IsTerminal(f.Fd()) {
		return false
	}
	return lipgloss.ColorProfile() != termenv.Ascii
}

func IsTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

type Style func(string) string

func plain(s string) string { return s }

func (e Env) Bold() Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return lgBold.Render(s) }
}

func (e Env) Dim() Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return lgFaint.Render(s) }
}

func terminalHeight(f *os.File) int {
	if _, h, err := term.GetSize(f.Fd()); err == nil && h > 0 {
		return h
	}
	return 24
}
