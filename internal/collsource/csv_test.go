package collsource

import (
	"os"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func parseFile(t *testing.T, name, format string) *Collection {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	c, err := Parse(f, format)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return c
}

func TestManaBoxIsSniffedAndKeepsBinders(t *testing.T) {
	c := parseFile(t, "manabox.csv", "auto")
	if c.Format != "manabox" {
		t.Fatalf("sniffed %q, want manabox", c.Format)
	}
	if len(c.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(c.Rows))
	}
	sol := c.Rows[0]
	if sol.Quantity != 2 || sol.Binder != "Trade Binder" || sol.Finish != "nonfoil" {
		t.Errorf("sol ring row = %+v", sol)
	}
	// ManaBox carries Scryfall IDs, so resolution must be exact.
	if sol.Ident != (scryfall.Identifier{ID: "sol-id-1"}) {
		t.Errorf("ident = %+v, want the Scryfall ID", sol.Ident)
	}
	if c.Rows[1].Finish != "foil" {
		t.Errorf("bolt finish = %q, want foil", c.Rows[1].Finish)
	}
	if c.Rows[2].Binder != "" {
		t.Errorf("remora binder = %q, want empty (default)", c.Rows[2].Binder)
	}
	// One excellent-condition row, one Japanese row, one purchase price.
	if c.Dropped["condition"] != 1 || c.Dropped["language"] != 1 || c.Dropped["purchase price"] != 1 {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestMoxfieldFallsBackToSetAndNumber(t *testing.T) {
	c := parseFile(t, "moxfield.csv", "auto")
	if c.Format != "moxfield" {
		t.Fatalf("sniffed %q, want moxfield", c.Format)
	}
	// Moxfield has no Scryfall ID column, so set+number is the scheme; the
	// name still rides along for the fallback pass.
	sol := c.Rows[0]
	if sol.Ident != (scryfall.Identifier{Set: "c21", CollectorNumber: "125"}) {
		t.Errorf("ident = %+v, want set+number", sol.Ident)
	}
	if sol.Name != "Sol Ring" {
		t.Errorf("name = %q", sol.Name)
	}
	if got := c.Rows[2].Name; got != "Borrowing 100,000 Arrows" {
		t.Errorf("quoted name = %q", got)
	}
	if c.Dropped["condition"] != 1 || c.Dropped["purchase price"] != 1 || c.Dropped["language"] != 0 {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestDelverParsesQuantityStylings(t *testing.T) {
	c := parseFile(t, "delver.csv", "auto")
	if c.Format != "delver" {
		t.Fatalf("sniffed %q, want delver", c.Format)
	}
	if c.Rows[0].Quantity != 2 { // "2x"
		t.Errorf("quantity = %d, want 2", c.Rows[0].Quantity)
	}
	// Row without a Scryfall ID falls to set+number; row with one uses it.
	if c.Rows[0].Ident != (scryfall.Identifier{Set: "c21", CollectorNumber: "125"}) {
		t.Errorf("ident = %+v", c.Rows[0].Ident)
	}
	if c.Rows[1].Ident != (scryfall.Identifier{ID: "bolt-id-1"}) {
		t.Errorf("ident = %+v", c.Rows[1].Ident)
	}
	if c.Rows[1].Finish != "foil" {
		t.Errorf("finish = %q, want foil", c.Rows[1].Finish)
	}
}

func TestHoardCanonicalRoundTripsContainers(t *testing.T) {
	c := parseFile(t, "hoard.csv", "auto")
	if c.Format != "hoard" {
		t.Fatalf("sniffed %q, want hoard", c.Format)
	}
	if c.Rows[1].Binder != "Trade" || c.Rows[1].Finish != "foil" || c.Rows[1].Kind != "binder" {
		t.Errorf("trade row = %+v", c.Rows[1])
	}
	// The deck row carries its kind so the importer can refuse to pour deck
	// contents into a binder.
	if c.Rows[3].Kind != "deck" || c.Rows[3].Binder != "Fish" {
		t.Errorf("deck row = %+v", c.Rows[3])
	}
	if len(c.Dropped) != 0 {
		t.Errorf("dropped = %v, want nothing — the canonical format is lossless", c.Dropped)
	}
}

// A canonical file from before the Container Kind column still parses, its
// rows all reading as binder rows — which is all such files ever held.
func TestHoardCanonicalWithoutKindColumnStillParses(t *testing.T) {
	in := "Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Board,Price USD\n" +
		"2,Sol Ring,c21,125,nonfoil,sol-id-1,Binder,main,2.00\n"
	c, err := Parse(strings.NewReader(in), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Format != "hoard" || len(c.Rows) != 1 || c.Rows[0].Kind != "" {
		t.Errorf("format=%q rows=%+v, want hoard rows with empty kind", c.Format, c.Rows)
	}
}

func TestUnknownHeaderFailsLoudlyAndSuggestsFormat(t *testing.T) {
	_, err := Parse(strings.NewReader("Qty,Card,Expansion\n1,Sol Ring,C21\n"), "auto")
	if err == nil {
		t.Fatal("unknown header parsed, want an error")
	}
	if !strings.Contains(err.Error(), "--format") || !strings.Contains(err.Error(), "Expansion") {
		t.Errorf("error %q should name the columns seen and suggest --format", err)
	}
}

func TestFormatOverrideBeatsSniffing(t *testing.T) {
	// A file missing every sniff column still parses when the caller names
	// the dialect, as long as the required columns exist.
	in := "Quantity,Name\n3,Sol Ring\n"
	c, err := Parse(strings.NewReader(in), "delver")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Rows[0].Quantity != 3 || c.Rows[0].Ident != (scryfall.Identifier{Name: "Sol Ring"}) {
		t.Errorf("row = %+v, want a name-only identifier", c.Rows[0])
	}
}

func TestParseErrorsCarryTheLineNumber(t *testing.T) {
	in := "Count,Tradelist Count,Name,Edition,Collector Number,Foil\n" +
		"1,0,Sol Ring,c21,125,\n" +
		"nope,0,Lightning Bolt,2x2,117,\n"
	_, err := Parse(strings.NewReader(in), "auto")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("err = %v, want it to blame line 3", err)
	}
}

func TestBOMAndEmptyFilesAreHandled(t *testing.T) {
	in := "\ufeffCount,Tradelist Count,Name,Edition,Collector Number\n1,0,Sol Ring,c21,125\n"
	if _, err := Parse(strings.NewReader(in), "auto"); err != nil {
		t.Errorf("BOM-prefixed file failed: %v", err)
	}
	if _, err := Parse(strings.NewReader(""), "auto"); err == nil {
		t.Error("empty file parsed, want an error")
	}
	header := "Count,Tradelist Count,Name,Edition,Collector Number\n"
	if _, err := Parse(strings.NewReader(header), "auto"); err == nil {
		t.Error("header-only file parsed, want a no-cards error")
	}
}

// normCondition is where two condition scales meet. Every value below was taken
// from a real export or is the abbreviation apps write for one.
func TestNormCondition(t *testing.T) {
	for in, want := range map[string]string{
		// TCGplayer's five — Moxfield, Delver, and hoard's own vocabulary.
		"Near Mint":      "nm",
		"NM":             "nm",
		"Lightly Played": "lp",
		"LP":             "lp",

		// Moxfield spells this one three ways and abbreviates it SP, not LP;
		// Cardsphere calls it Slightly Played. Every one of these appears in a
		// real export and every one used to read as unknown.
		"Good (Lightly Played)": "lp",
		"Good/Lightly Played":   "lp",
		"SP":                    "lp",
		"Slightly Played":       "lp",

		"Moderately Played": "mp",
		"MP":                "mp",
		"Heavily Played":    "hp",
		"HP":                "hp",
		"Damaged":           "dmg",
		"DMG":               "dmg",

		// Cardmarket's seven, as ManaBox writes them. Mint folds down because
		// neither MTGJSON nor TCGplayer has anything above near mint.
		"mint":         "nm",
		"near_mint":    "nm",
		"excellent":    "lp",
		"good":         "lp",
		"light_played": "lp",
		"played":       "mp",
		"poor":         "dmg",

		// Nothing said, and something said that no scale uses. Both read as
		// unknown: a condition hoard cannot place is not one it should invent.
		"":           "unknown",
		"   ":        "unknown",
		"Pristine":   "unknown",
		"graded 9.5": "unknown",
		"BGS 10":     "unknown",
	} {
		if got := normCondition(in); got != want {
			t.Errorf("normCondition(%q) = %q, want %q", in, got, want)
		}
	}
}

// The one genuine collision between the scales, called out so a future change
// cannot quietly pick the other reading. Cardmarket's "light played" sits a
// step below TCGplayer's "lightly played", and the strings are nearly the
// same; both fold to lp, the commoner reading. It mislabels rather than
// misprices, because condition does not affect value.
func TestNormConditionFoldsTheAmbiguousLightPlayed(t *testing.T) {
	if got := normCondition("lightly played"); got != "lp" {
		t.Errorf("TCGplayer lightly played = %q, want lp", got)
	}
	if got := normCondition("light played"); got != "lp" {
		t.Errorf("Cardmarket light played = %q, want lp (the commoner reading)", got)
	}
}

// An import reports what it could not carry exactly, so a seven-value export
// folded onto five says so rather than arriving quietly. A blank cell is the
// ordinary case and is not a loss; near mint is what hoard would have assumed
// anyway; anything else — folded or unplaceable — is worth a line.
func TestInformativeConditionReportsWhatWasLost(t *testing.T) {
	for _, quiet := range []string{"", "  ", "Near Mint", "near_mint", "NM", "mint"} {
		if informativeCondition(quiet) {
			t.Errorf("informativeCondition(%q) = true, want quiet", quiet)
		}
	}
	for _, loud := range []string{"Lightly Played", "excellent", "good", "played", "poor", "Pristine"} {
		if !informativeCondition(loud) {
			t.Errorf("informativeCondition(%q) = false, want reported", loud)
		}
	}
}
