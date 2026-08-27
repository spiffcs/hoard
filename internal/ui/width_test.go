package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWidthAgreesWithAnsiStringWidth(t *testing.T) {
	corpus := []string{
		"", " ", "a", "Sol Ring", "$1,234.56", "-$0.01", "—", "mh2 · 138",
		"Ragavan, Nimble Pilferer", "Æther Vial", "Márton Stromgald",
		"Jötun Grunt", "東京", "🜁🜂", "a\tb", "a\nb", "\x7f",
		"\x1b[31mred\x1b[0m", "\x1b[2Kcleared", strings.Repeat("x", 200),
		"NM", "nonfoil", "1,234", "999", "0.00", "é", "👩‍👩‍👦",
	}
	for _, s := range corpus {
		if got, want := Width(s), ansi.StringWidth(s); got != want {
			t.Errorf("Width(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

func TestWidthMatchesOnGeneratedCardStrings(t *testing.T) {
	for i := range 5000 {
		for _, s := range []string{
			Money(float64(i) / 7),
			MoneyPtr(nil),
			Count(i * 37),
			Printing("mh2", string(rune('a'+i%26))+"-42"),
			Qty(i % 99),
		} {
			if got, want := Width(s), ansi.StringWidth(s); got != want {
				t.Fatalf("Width(%q) = %d, ansi.StringWidth = %d", s, got, want)
			}
		}
	}
}
