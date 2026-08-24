package collsource

import (
	"os"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
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
	if sol.Quantity != 2 || sol.Binder != "Trade Binder" || sol.Finish != finish.Nonfoil {
		t.Errorf("sol ring row = %+v", sol)
	}

	if sol.Ident != (scryfall.Identifier{ID: "sol-id-1"}) {
		t.Errorf("ident = %+v, want the Scryfall ID", sol.Ident)
	}
	if c.Rows[1].Finish != finish.Foil {
		t.Errorf("bolt finish = %q, want foil", c.Rows[1].Finish)
	}
	if c.Rows[2].Binder != "" {
		t.Errorf("remora binder = %q, want empty (default)", c.Rows[2].Binder)
	}

	if c.Rows[1].Condition != "lp" {
		t.Errorf("bolt condition = %q, want excellent folded onto lp", c.Rows[1].Condition)
	}
	if c.Dropped["condition"] != 0 || c.Dropped["language"] != 1 || c.Dropped["purchase price"] != 1 {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestMoxfieldFallsBackToSetAndNumber(t *testing.T) {
	c := parseFile(t, "moxfield.csv", "auto")
	if c.Format != "moxfield" {
		t.Fatalf("sniffed %q, want moxfield", c.Format)
	}

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

	if c.Dropped["condition"] != 0 || c.Dropped["purchase price"] != 1 || c.Dropped["language"] != 0 {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestDelverParsesQuantityStylings(t *testing.T) {
	c := parseFile(t, "delver.csv", "auto")
	if c.Format != "delver" {
		t.Fatalf("sniffed %q, want delver", c.Format)
	}
	if c.Rows[0].Quantity != 2 {
		t.Errorf("quantity = %d, want 2", c.Rows[0].Quantity)
	}

	if c.Rows[0].Ident != (scryfall.Identifier{Set: "c21", CollectorNumber: "125"}) {
		t.Errorf("ident = %+v", c.Rows[0].Ident)
	}
	if c.Rows[1].Ident != (scryfall.Identifier{ID: "bolt-id-1"}) {
		t.Errorf("ident = %+v", c.Rows[1].Ident)
	}
	if c.Rows[1].Finish != finish.Foil {
		t.Errorf("finish = %q, want foil", c.Rows[1].Finish)
	}
}

func TestParseQtyRejectsDoubleX(t *testing.T) {
	for _, ok := range []struct {
		in   string
		want int
	}{{"2", 2}, {"2x", 2}, {"x2", 2}, {"X4", 4}} {
		if n, err := parseQty(ok.in); err != nil || n != ok.want {
			t.Errorf("parseQty(%q) = %d, %v; want %d", ok.in, n, err, ok.want)
		}
	}
	for _, bad := range []string{"x2x", "xx2", "2xx", "x", ""} {
		if n, err := parseQty(bad); err == nil {
			t.Errorf("parseQty(%q) = %d, want an error", bad, n)
		}
	}
}

func TestGenericCardNumberColumnIsNotDelver(t *testing.T) {
	in := "Count,Name,Card number\n2,Sol Ring,125\n"
	if _, err := Parse(strings.NewReader(in), "auto"); err == nil ||
		!strings.Contains(err.Error(), "unrecognized CSV header") {
		t.Errorf("err = %v, want the unrecognized-header refusal", err)
	}
}

func TestHoardCanonicalRoundTripsContainers(t *testing.T) {
	c := parseFile(t, "hoard.csv", "auto")
	if c.Format != "hoard" {
		t.Fatalf("sniffed %q, want hoard", c.Format)
	}
	if c.Rows[1].Binder != "Trade" || c.Rows[1].Finish != finish.Foil || c.Rows[1].Kind != "binder" {
		t.Errorf("trade row = %+v", c.Rows[1])
	}

	if c.Rows[3].Kind != "deck" || c.Rows[3].Binder != "Fish" {
		t.Errorf("deck row = %+v", c.Rows[3])
	}
	if len(c.Dropped) != 0 {
		t.Errorf("dropped = %v, want nothing — the canonical format is lossless", c.Dropped)
	}
}

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

const overlongHeader = "Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID,Container,Container Kind,Board,Price USD\n"

func TestOverlongRowIsRefusedBeforeAnythingResolves(t *testing.T) {
	in := overlongHeader +
		"1,Shalai, Voice of Plenty,dom,35,foil,,db827ee7-6f2e-4e10-aac0-120fc2b69fbd,Binder,binder,main,5.08\n"
	_, err := Parse(strings.NewReader(in), "auto")
	if err == nil {
		t.Fatal("a 12-field row under an 11-field header parsed, want a refusal")
	}
	for _, want := range []string{"line 2", "12 fields", "header has 11", "unquoted comma"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestQuotedCommaInANameStillParses(t *testing.T) {
	in := overlongHeader +
		`1,"Shalai, Voice of Plenty",dom,35,foil,,db827ee7-6f2e-4e10-aac0-120fc2b69fbd,Binder,binder,main,5.08` + "\n"
	c, err := Parse(strings.NewReader(in), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Rows[0].Name; got != "Shalai, Voice of Plenty" {
		t.Errorf("name = %q, want the comma kept", got)
	}
	if c.Rows[0].Ident != (scryfall.Identifier{ID: "db827ee7-6f2e-4e10-aac0-120fc2b69fbd"}) {
		t.Errorf("ident = %+v, want the Scryfall ID unshifted", c.Rows[0].Ident)
	}
}

func TestShortRowsAreStillTolerated(t *testing.T) {
	c, err := Parse(strings.NewReader(overlongHeader+"1,Sol Ring,c21,125\n"), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Rows) != 1 || c.Rows[0].Finish != finish.Nonfoil || c.Rows[0].Binder != "" {
		t.Errorf("row = %+v, want the absent cells to read as empty", c.Rows[0])
	}
	if c.Rows[0].Ident != (scryfall.Identifier{Set: "c21", CollectorNumber: "125"}) {
		t.Errorf("ident = %+v, want set+number", c.Rows[0].Ident)
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

func TestNormCondition(t *testing.T) {
	for in, want := range map[string]string{

		"Near Mint":      "nm",
		"NM":             "nm",
		"Lightly Played": "lp",
		"LP":             "lp",

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

		"mint":         "nm",
		"near_mint":    "nm",
		"excellent":    "lp",
		"good":         "lp",
		"light_played": "lp",
		"played":       "mp",
		"poor":         "dmg",

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

func TestNormConditionFoldsTheAmbiguousLightPlayed(t *testing.T) {
	if got := normCondition("lightly played"); got != "lp" {
		t.Errorf("TCGplayer lightly played = %q, want lp", got)
	}
	if got := normCondition("light played"); got != "lp" {
		t.Errorf("Cardmarket light played = %q, want lp (the commoner reading)", got)
	}
}

func TestUnplaceableConditionReportsOnlyWhatWasLost(t *testing.T) {
	for _, carried := range []string{
		"", "  ", "Near Mint", "near_mint", "NM", "mint",
		"Lightly Played", "excellent", "good", "played", "poor",
	} {
		if unplaceableCondition(carried) {
			t.Errorf("unplaceableCondition(%q) = true, but it is stored", carried)
		}
	}

	for _, lost := range []string{"Pristine", "BGS 10", "PSA 9", "graded 9.5"} {
		if !unplaceableCondition(lost) {
			t.Errorf("unplaceableCondition(%q) = false, want it reported", lost)
		}
	}
}

func TestParseCarriesTheCondition(t *testing.T) {
	const manabox = "Binder Name,Name,Set code,Collector number,Foil,Quantity,Scryfall ID,Condition,Language\n" +
		"Trade,Sol Ring,C21,125,normal,2,sol-id,near_mint,en\n" +
		"Trade,Lightning Bolt,2X2,117,foil,1,bolt-id,excellent,en\n" +
		"Trade,Mox Pearl,LEA,265,normal,1,mox-id,,en\n" +
		"Trade,Black Lotus,LEA,232,normal,1,lotus-id,BGS 10,en\n"

	c, err := Parse(strings.NewReader(manabox), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"nm", "lp", "unknown", "unknown"}
	for i, w := range want {
		if c.Rows[i].Condition != w {
			t.Errorf("row %d condition = %q, want %q", i, c.Rows[i].Condition, w)
		}
	}

	if c.Dropped["condition"] != 1 {
		t.Errorf("dropped %d conditions, want 1 (the BGS grade alone): %v",
			c.Dropped["condition"], c.Dropped)
	}
}

func TestCanonicalRoundTripsCondition(t *testing.T) {
	const withCond = "Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID,Container,Container Kind,Board,Price USD\n" +
		"2,Sol Ring,c21,125,nonfoil,lp,sol-id,Binder,binder,main,2.00\n" +
		"1,Sol Ring,c21,125,nonfoil,,sol-id,Binder,binder,main,2.00\n"
	c, err := Parse(strings.NewReader(withCond), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Format != "hoard" {
		t.Fatalf("format = %q, want the canonical sniff to still match", c.Format)
	}
	if c.Rows[0].Condition != "lp" || c.Rows[1].Condition != "unknown" {
		t.Errorf("conditions = %q/%q, want lp and unknown",
			c.Rows[0].Condition, c.Rows[1].Condition)
	}
	if len(c.Dropped) != 0 {
		t.Errorf("dropped %v, want nothing: the canonical format is lossless", c.Dropped)
	}

	const noCond = "Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Container Kind,Board,Price USD\n" +
		"2,Sol Ring,c21,125,nonfoil,sol-id,Binder,binder,main,2.00\n"
	old, err := Parse(strings.NewReader(noCond), "auto")
	if err != nil {
		t.Fatalf("Parse (pre-column): %v", err)
	}
	if old.Rows[0].Condition != "unknown" {
		t.Errorf("pre-column row = %q, want unknown", old.Rows[0].Condition)
	}
}
