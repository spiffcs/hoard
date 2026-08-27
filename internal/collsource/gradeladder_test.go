package collsource

import (
	"strings"
	"testing"
)

func conditionsFor(t *testing.T, format, in string) []string {
	t.Helper()
	c, err := Parse(strings.NewReader(in), format)
	if err != nil {
		t.Fatalf("Parse(%s): %v", format, err)
	}
	out := make([]string, len(c.Rows))
	for i, r := range c.Rows {
		out[i] = r.Condition
	}
	return out
}

func TestManaBoxGradeLadderWalksHoardsOwn(t *testing.T) {
	header := "Binder Name,Binder Type,Name,Set code,Set name,Collector number," +
		"Foil,Rarity,Quantity,ManaBox ID,Scryfall ID,Purchase price,Misprint," +
		"Altered,Condition,Language,Purchase price currency\n"
	row := func(grade string) string {
		return "B,binder,Sol Ring,C21,Commander 2021,125,normal,uncommon,1,1," +
			"sol-id-1,0,false,false," + grade + ",en,USD\n"
	}

	grades := []string{"near_mint", "excellent", "good", "light_played", "played", "poor"}
	want := []string{"nm", "nm", "lp", "mp", "hp", "dmg"}

	in := header
	for _, g := range grades {
		in += row(g)
	}

	got := conditionsFor(t, "manabox", in)
	if len(got) != len(want) {
		t.Fatalf("parsed %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("manabox %q = %q, want %q", grades[i], got[i], want[i])
		}
	}
}

func TestPlayedMeansDifferentThingsToDifferentVendors(t *testing.T) {
	manabox := "Binder Name,Binder Type,Name,Set code,Set name,Collector number," +
		"Foil,Rarity,Quantity,ManaBox ID,Scryfall ID,Purchase price,Misprint," +
		"Altered,Condition,Language,Purchase price currency\n" +
		"B,binder,Sol Ring,C21,Commander 2021,125,normal,uncommon,1,1," +
		"sol-id-1,0,false,false,played,en,USD\n"

	moxfield := "Count,Tradelist Count,Name,Edition,Condition,Language,Foil,Tags," +
		"Last Modified,Collector Number,Alter,Proxy,Purchase Price\n" +
		"1,0,Sol Ring,c21,Played,English,,,2026-01-15 08:00:00.000000,125,False,False,\n"

	if got := conditionsFor(t, "manabox", manabox); got[0] != "hp" {
		t.Errorf("manabox played = %q, want hp — it sits below light_played "+
			"on a six-grade ladder", got[0])
	}
	if got := conditionsFor(t, "moxfield", moxfield); got[0] != "mp" {
		t.Errorf("moxfield Played = %q, want mp — its ladder is hoard's own, "+
			"and remapping it would silently re-grade every Moxfield import", got[0])
	}
}

func TestManaBoxLightPlayedDoesNotDragTCGplayerLightlyPlayedDown(t *testing.T) {
	moxfield := "Count,Tradelist Count,Name,Edition,Condition,Language,Foil,Tags," +
		"Last Modified,Collector Number,Alter,Proxy,Purchase Price\n" +
		"1,0,Sol Ring,c21,Lightly Played,English,,,2026-01-15 08:00:00.000000,125,False,False,\n"

	if got := conditionsFor(t, "moxfield", moxfield); got[0] != "lp" {
		t.Errorf("moxfield Lightly Played = %q, want lp", got[0])
	}
}
