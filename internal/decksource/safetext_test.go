package decksource

import (
	"strings"
	"testing"
)

func TestParseTextCleansEveryStringItReturns(t *testing.T) {

	const osc = "\x1b]52;c;cGF5bG9hZA==\x07"
	list := "1 Sol Ring" + osc + " (LEA" + osc + ") 1\n" +
		"%%%" + osc + " unparseable\n" +
		"1 Black Lotus\n"

	d, err := ParseText("Burn"+osc, "", "", "text", strings.NewReader(list))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}

	for _, f := range []struct{ what, got string }{
		{"deck name", d.Name},
		{"source id", d.SourceID},
		{"source url", d.SourceURL},
		{"format", d.Format},
	} {
		if strings.ContainsRune(f.got, 0x1b) {
			t.Errorf("%s kept an ESC: %q", f.what, f.got)
		}
	}
	for i, e := range d.Entries {
		for _, f := range []struct{ what, got string }{
			{"entry name", e.Name},
			{"ident name", e.Ident.Name},
			{"ident set", e.Ident.Set},
			{"ident number", e.Ident.CollectorNumber},
		} {
			if strings.ContainsRune(f.got, 0x1b) {
				t.Errorf("entry %d %s kept an ESC: %q", i, f.what, f.got)
			}
		}
	}

	if len(d.Skipped) == 0 {
		t.Fatal("expected the malformed line to be skipped; the fixture no longer " +
			"exercises the Skipped sink")
	}
	for i, s := range d.Skipped {
		if strings.ContainsRune(s, 0x1b) {
			t.Errorf("skipped[%d] kept an ESC: %q", i, s)
		}
	}
}

func TestParseTextCleansTheErrorItQuotes(t *testing.T) {
	const osc = "\x1b]52;c;cGF5bG9hZA==\x07"
	_, err := ParseText("d", "", "", "text", strings.NewReader("%%%"+osc+"\n"))
	if err == nil {
		t.Fatal("expected an error for a decklist with no readable cards")
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("the error message kept an ESC: %q", err.Error())
	}
}

func TestParseLooseCleansEntriesAndSkipped(t *testing.T) {
	const osc = "\x1b]52;c;cGF5bG9hZA==\x07"
	entries, skipped, err := ParseLoose(strings.NewReader(
		"1 Sol Ring" + osc + "\n%%%" + osc + "\n"))
	if err != nil {
		t.Fatalf("ParseLoose: %v", err)
	}
	for i, e := range entries {
		if strings.ContainsRune(e.Name, 0x1b) || strings.ContainsRune(e.Ident.Name, 0x1b) {
			t.Errorf("entry %d kept an ESC: %q / %q", i, e.Name, e.Ident.Name)
		}
	}
	for i, s := range skipped {
		if strings.ContainsRune(s, 0x1b) {
			t.Errorf("skipped[%d] kept an ESC: %q", i, s)
		}
	}
}
