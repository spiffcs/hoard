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
	if sol.Quantity != 2 || sol.Binder != "Trade Binder" || sol.Finish != "normal" {
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
	if c.Rows[1].Binder != "Trade" || c.Rows[1].Finish != "foil" {
		t.Errorf("trade row = %+v", c.Rows[1])
	}
	if len(c.Dropped) != 0 {
		t.Errorf("dropped = %v, want nothing — the canonical format is lossless", c.Dropped)
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
