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
		// Zero is plural in English: "0 card" is the reading a naive
		// n > 1 test would produce, and it is wrong.
		{0, "card", "cards", "0 cards"},
		// Irregular, which is why both forms are spelled out rather than
		// derived by appending an "s".
		{1, "watch", "watches", "1 watch"},
		{3, "watch", "watches", "3 watches"},
		// The verb agrees too, and the parameters are meant to carry it.
		{1, "line was", "lines were", "1 line was"},
		{4, "line was", "lines were", "4 lines were"},
	} {
		if got := Plural(tc.n, tc.singular, tc.plural); got != tc.want {
			t.Errorf("Plural(%d, %q, %q) = %q, want %q",
				tc.n, tc.singular, tc.plural, got, tc.want)
		}
	}
}

// Prose counts without thousands separators, unlike a table column. Count
// exists for the column case and must not leak into a sentence.
func TestPluralDoesNotGroupThousands(t *testing.T) {
	got := Plural(1200, "card", "cards")
	if strings.Contains(got, ",") {
		t.Errorf("Plural grouped a prose count: %q", got)
	}
	if got != "1200 cards" {
		t.Errorf("Plural(1200) = %q, want %q", got, "1200 cards")
	}
}

// The two things hoard calls "identity" and "id" are unrelated, and the
// header over a pips column must not be the one a binder row answers to.
func TestHeaderIdentityIsNotAnIdentifier(t *testing.T) {
	if HeaderIdentity == "ID" {
		t.Fatal("the color-identity column still shares its header with the binder row id")
	}
	// It must also not claim to be the card's color: color identity and
	// color are different facts, and a colorless land can have one.
	if HeaderIdentity == "COLOR" {
		t.Error("COLOR states a card's color, which is not its color identity")
	}
	// A five-color card already renders WUBRG, so the header can never be
	// the thing that widens the column.
	if len(HeaderIdentity) > len(Pips([]string{"W", "U", "B", "R", "G"})) {
		t.Errorf("header %q is wider than the widest cell %q it sits over",
			HeaderIdentity, Pips([]string{"W", "U", "B", "R", "G"}))
	}
}
