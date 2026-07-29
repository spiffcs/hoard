package decksource

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"
)

func TestArchidektMatches(t *testing.T) {
	p := archidektProvider{}
	for _, raw := range []string{
		"https://archidekt.com/decks/7319967/foo",
		"https://www.archidekt.com/decks/7319967",
	} {
		u, _ := url.Parse(raw)
		if !p.Matches(u) {
			t.Errorf("should match %q", raw)
		}
	}
	u, _ := url.Parse("https://moxfield.com/decks/abc")
	if p.Matches(u) {
		t.Error("should not match moxfield.com")
	}
}

func TestParseArchidektID(t *testing.T) {
	u, _ := url.Parse("https://archidekt.com/decks/7319967/high_power")
	id, err := parseArchidektID(u)
	if err != nil || id != "7319967" {
		t.Fatalf("got id=%q err=%v, want 7319967", id, err)
	}
	bad, _ := url.Parse("https://archidekt.com/decks/not-a-number")
	if _, err := parseArchidektID(bad); err == nil {
		t.Error("expected error for non-numeric id")
	}
}

func TestArchidektToDeckFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/archidekt_7319967.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var ad archidektDeck
	if err := json.Unmarshal(raw, &ad); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	d := archidektToDeck("7319967", "https://archidekt.com/decks/7319967", &ad)

	if d.Source != "archidekt" || d.SourceID != "7319967" {
		t.Errorf("source metadata wrong: %+v", d)
	}
	if d.Format != "Commander/EDH" {
		t.Errorf("format = %q, want Commander/EDH", d.Format)
	}
	if len(d.Entries) != 126 {
		t.Fatalf("entries = %d, want 126", len(d.Entries))
	}

	// Board + finish distribution should match the fixture probe.
	boards := map[string]int{}
	finishes := map[string]int{}
	var commanderName string
	for _, e := range d.Entries {
		boards[e.Board]++
		finishes[e.Finish]++
		if e.Board == BoardCommander {
			// Every entry carries a Scryfall ID from card.uid.
			if e.Ident.ID == "" {
				t.Error("commander entry missing Scryfall ID")
			}
			commanderName = e.Ident.ID
		}
	}
	if boards[BoardCommander] != 1 {
		t.Errorf("commander count = %d, want 1", boards[BoardCommander])
	}
	if boards[BoardMaybe] != 26 {
		t.Errorf("maybeboard count = %d, want 26", boards[BoardMaybe])
	}
	if finishes["foil"] != 29 || finishes["etched"] != 2 {
		t.Errorf("finishes = %v, want foil=29 etched=2", finishes)
	}
	if commanderName == "" {
		t.Error("no commander identifier captured")
	}
	// Every entry should carry a Scryfall ID (Archidekt provides card.uid).
	for _, e := range d.Entries {
		if e.Ident.ID == "" {
			t.Errorf("entry %v missing Scryfall ID", e.Ident)
		}
	}
}
