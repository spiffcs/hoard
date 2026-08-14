package ui

import (
	"strings"
	"testing"
)

func TestPluralAgreesWithItsCount(t *testing.T) {
	for _, tc := range []struct {
		n                int
		singular, plural string
		want             string
	}{
		{1, "card", "cards", "1 card"},
		{2, "card", "cards", "2 cards"},

		{0, "card", "cards", "0 cards"},

		{1, "watch", "watches", "1 watch"},
		{3, "watch", "watches", "3 watches"},

		{1, "line was", "lines were", "1 line was"},
		{4, "line was", "lines were", "4 lines were"},
	} {
		if got := Plural(tc.n, tc.singular, tc.plural); got != tc.want {
			t.Errorf("Plural(%d, %q, %q) = %q, want %q",
				tc.n, tc.singular, tc.plural, got, tc.want)
		}
	}
}

func TestPluralDoesNotGroupThousands(t *testing.T) {
	got := Plural(1200, "card", "cards")
	if strings.Contains(got, ",") {
		t.Errorf("Plural grouped a prose count: %q", got)
	}
	if got != "1200 cards" {
		t.Errorf("Plural(1200) = %q, want %q", got, "1200 cards")
	}
}

func TestHeaderIdentityIsNotAnIdentifier(t *testing.T) {
	if HeaderIdentity == "ID" {
		t.Fatal("the color-identity column still shares its header with the binder row id")
	}

	if HeaderIdentity == "COLOR" {
		t.Error("COLOR states a card's color, which is not its color identity")
	}

	if len(HeaderIdentity) > len(Pips([]string{"W", "U", "B", "R", "G"})) {
		t.Errorf("header %q is wider than the widest cell %q it sits over",
			HeaderIdentity, Pips([]string{"W", "U", "B", "R", "G"}))
	}
}
