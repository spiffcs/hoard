package safetext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanRemovesWhatATerminalActsOn(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{

		{"OSC 52 clipboard write", "Burn\x1b]52;c;cGF5bG9hZA==\x07", "Burn]52;c;cGF5bG9hZA=="},
		{"OSC window retitle", "Burn\x1b]0;pwned\x07", "Burn]0;pwned"},
		{"CSI colour", "Burn\x1b[31mRED", "Burn[31mRED"},

		{"newline forges a row", "Burn\n1 Sol Ring", "Burn1 Sol Ring"},
		{"carriage return repaints", "real\rfake", "realfake"},
		{"NUL truncates downstream", "Burn\x00hidden", "Burnhidden"},
		{"tab", "a\tb", "ab"},
		{"DEL", "a\x7fb", "ab"},

		{"C1 CSI", "Burn31m", "Burn31m"},

		{"bidi override", "Burn‮gnaeM", "BurngnaeM"},
		{"bidi isolate", "Burn⁦x⁩", "Burnx"},

		{"plain", "Modern Burn", "Modern Burn"},
		{"punctuation", "Urza's Saga — 4C", "Urza's Saga — 4C"},
		{"non-latin", "青い護符デッキ", "青い護符デッキ"},
		{"emoji", "Burn 🔥", "Burn 🔥"},
		{"accents", "Dáin Ironfoot", "Dáin Ironfoot"},

		{"literal replacement char", "a�b", "a�b"},

		{"trims", "  Burn  ", "Burn"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Clean(c.in); got != c.want {
				t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCleanReplacesInvalidUTF8(t *testing.T) {

	got := Clean("Burn\xffX")
	if !utf8.ValidString(got) {
		t.Fatalf("Clean produced invalid UTF-8: %q", got)
	}
	if got != "Burn�X" {
		t.Errorf("Clean = %q, want %q", got, "Burn�X")
	}
}

func TestCleanBoundsLength(t *testing.T) {

	for _, r := range []string{"a", "護"} {
		in := strings.Repeat(r, MaxNameRunes*2)
		got := Clean(in)
		if n := utf8.RuneCountInString(got); n != MaxNameRunes {
			t.Errorf("Clean(%d×%q) kept %d runes, want %d",
				MaxNameRunes*2, r, n, MaxNameRunes)
		}
	}
}

func TestOkAgreesWithClean(t *testing.T) {
	for _, s := range []string{
		"Modern Burn", "", "  x  ", "a\x1bb", "護符", "a�b", "a\xffb",
		strings.Repeat("a", MaxNameRunes), strings.Repeat("a", MaxNameRunes+1),
	} {
		if ok(s) != (Clean(s) == s) {
			t.Errorf("ok(%q) = %v but Clean returned %q", s, ok(s), Clean(s))
		}
	}
}

func FuzzCleanLeavesNothingInterpreted(f *testing.F) {
	for _, s := range []string{"Burn", "\x1b]52;c;x\x07", "‮", "\xff", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Clean(s)
		if !utf8.ValidString(got) {
			t.Fatalf("Clean(%q) produced invalid UTF-8: %q", s, got)
		}
		for _, r := range got {
			if interpreted(r) {
				t.Fatalf("Clean(%q) kept %U, which a terminal acts on", s, r)
			}
		}
		if n := utf8.RuneCountInString(got); n > MaxNameRunes {
			t.Fatalf("Clean(%q) returned %d runes, over the %d bound", s, n, MaxNameRunes)
		}

		if again := Clean(got); again != got {
			t.Fatalf("Clean not idempotent: %q -> %q -> %q", s, got, again)
		}
	})
}
