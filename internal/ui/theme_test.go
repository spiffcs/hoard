package ui

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestMain pins the process-wide color state lipgloss caches on first use:
// go test's stdout is not a terminal, so without this every style renders
// as a no-op and the styling tests would assert nothing. Dark background is
// pinned too so adaptive colors resolve deterministically. Tests that need
// a different profile (degradation) set and restore their own.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	os.Exit(m.Run())
}

// Every theme style must be SGR-only — styling may never change a string's
// display width, because the table engine measures text before styling it.
// A Width, Padding or Margin smuggled into one style would silently break
// every layout that renders through it.
func TestThemeStylesAreSGROnly(t *testing.T) {
	samples := []string{"", "x", "Ragavan, Nimble Pilferer", "≈▁▂█ · — ×3"}
	check := func(name string, render func(string) string) {
		t.Helper()
		for _, s := range samples {
			if got, want := ansi.StringWidth(render(s)), ansi.StringWidth(s); got != want {
				t.Errorf("%s changes width of %q: %d, want %d", name, s, got, want)
			}
		}
	}

	v := reflect.ValueOf(DefaultTheme())
	for i := range v.NumField() {
		field := v.Type().Field(i)
		switch st := v.Field(i).Interface().(type) {
		case lipgloss.Style:
			check("Theme."+field.Name, func(s string) string { return st.Render(s) })
		case map[byte]lipgloss.Style:
			for letter, ps := range st {
				check(fmt.Sprintf("Theme.%s[%c]", field.Name, letter),
					func(s string) string { return ps.Render(s) })
			}
		default:
			t.Fatalf("Theme.%s has type %T — extend this invariant test to cover it",
				field.Name, st)
		}
	}

	e := Env{Color: true}
	for name, style := range map[string]Style{
		"Bold":                e.Bold(),
		"Dim":                 e.Dim(),
		"OK":                  e.OK(),
		"Err":                 e.Err(),
		"Warn":                e.Warn(),
		"Gain":                e.Gain(),
		"Loss":                e.Loss(),
		"Accent":              e.Accent(),
		"Pip(W)":              e.Pip('W'),
		"Pip(?)":              e.Pip('?'),
		"Identity(mono)":      e.Identity([]string{"G"}),
		"Identity(multi)":     e.Identity([]string{"W", "U"}),
		"Identity(colorless)": e.Identity([]string{}),
		"Identity(unknown)":   e.Identity(nil),
	} {
		check("Env."+name, style)
	}
}

// With color off, every style is the identity function — piped output must
// never see an escape sequence.
func TestEnvStylesPlainWithoutColor(t *testing.T) {
	e := Env{Color: false}
	for name, style := range map[string]Style{
		"OK":       e.OK(),
		"Err":      e.Err(),
		"Warn":     e.Warn(),
		"Gain":     e.Gain(),
		"Loss":     e.Loss(),
		"Accent":   e.Accent(),
		"Pip":      e.Pip('W'),
		"Identity": e.Identity([]string{"W", "U"}),
	} {
		if got := style("swamp"); got != "swamp" {
			t.Errorf("%s with Color:false = %q, want the text untouched", name, got)
		}
	}
}

// Identity resolution: one color tints with that color, several read gold,
// a known-empty identity reads wastes, and an unknown identity (nil — the
// card's document was never stored) stays plain rather than claiming
// colorlessness.
func TestIdentityResolution(t *testing.T) {
	e := Env{Color: true}
	tinted := func(c lipgloss.AdaptiveColor) string {
		return lipgloss.NewStyle().Foreground(c).Render("Name")
	}
	cases := []struct {
		name   string
		colors []string
		want   string
	}{
		{"mono white", []string{"W"}, tinted(identityW)},
		{"lowercase", []string{"u"}, tinted(identityU)},
		{"multicolor", []string{"W", "U"}, tinted(identityMulti)},
		{"colorless", []string{}, tinted(identityC)},
		{"unknown", nil, "Name"},
		{"junk letter", []string{"Z"}, "Name"},
	}
	for _, tc := range cases {
		if got := e.Identity(tc.colors)("Name"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if plain, w := "Name", e.Identity([]string{"W"})("Name"); w == plain {
		t.Error("a mono identity rendered unstyled — the profile pin is not in effect")
	}
}

// At a 16-color profile the pastels must degrade to ANSI indexes — still
// styled, never raw truecolor sequences a 16-color terminal would garble.
func TestThemeDegradesAtANSI(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	e := Env{Color: true}
	for name, style := range map[string]Style{
		"Identity": e.Identity([]string{"U"}),
		"Pip":      e.Pip('R'),
		"Err":      e.Err(),
		"Accent":   e.Accent(),
	} {
		got := style("x")
		if strings.Contains(got, "38;2;") {
			t.Errorf("%s emits truecolor at the ANSI profile: %q", name, got)
		}
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("%s renders unstyled at the ANSI profile: %q", name, got)
		}
		if got, want := ansi.StringWidth(got), 1; got != want {
			t.Errorf("%s width %d at ANSI, want %d", name, got, want)
		}
	}
}
