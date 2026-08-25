package compendium

import (
	"slices"
	"strings"
	"testing"
)

func TestFormatSuppliesLegalityAndNothingElse(t *testing.T) {
	o, err := ApplyFormat(Options{}, "premodern", false)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "premodern" {
		t.Errorf("Legal = %q, want premodern", o.Legal)
	}
	if len(o.Sets) != 0 {
		t.Errorf("without --era a format must not pin sets, so reprints of legal cards "+
			"stay in; got %d sets: %v", len(o.Sets), o.Sets)
	}
}

func TestNoFormatPinsSetsWithoutTheEraFlag(t *testing.T) {
	for _, name := range knownFormats {
		o, err := ApplyFormat(Options{}, name, false)
		if err != nil {
			t.Errorf("ApplyFormat(%q): %v", name, err)
			continue
		}
		if o.Legal != name {
			t.Errorf("--format %s gave Legal = %q, want %q", name, o.Legal, name)
		}
		if len(o.Sets) != 0 {
			t.Errorf("--format %s should filter on legality alone, got sets %v", name, o.Sets)
		}
	}
}

func TestEraPinsTheFormatsOwnSets(t *testing.T) {
	o, err := ApplyFormat(Options{}, "premodern", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "premodern" {
		t.Errorf("Legal = %q, want premodern", o.Legal)
	}
	if len(o.Sets) != 29 {
		t.Errorf("got %d sets, want the 29 from Fourth Edition through Scourge: %v",
			len(o.Sets), o.Sets)
	}
	for _, want := range []string{"4ed", "ice", "mir", "tmp", "usg", "mmq", "inv", "ody", "scg"} {
		if !slices.Contains(o.Sets, want) {
			t.Errorf("set %q missing from the premodern era: %v", want, o.Sets)
		}
	}
	for _, unwanted := range []string{"8ed", "mh2", "por", "s99"} {
		if slices.Contains(o.Sets, unwanted) {
			t.Errorf("set %q is not premodern-era but was included", unwanted)
		}
	}
}

func TestEraIsRefusedForAFormatThatHasNone(t *testing.T) {
	for _, name := range []string{"legacy", "modern", "commander", "vintage", "pauper"} {
		_, err := ApplyFormat(Options{}, name, true)
		if err == nil {
			t.Errorf("--format %s --era must be refused: %s has no bounded set list, "+
				"so there is no era to pin", name, name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal should name the format, got %q", err)
		}
		if !strings.Contains(err.Error(), "premodern") {
			t.Errorf("the refusal should name the formats that do have an era, got %q", err)
		}
	}
}

func TestEraWithoutAFormatIsRefused(t *testing.T) {
	_, err := ApplyFormat(Options{Rarities: []string{"rare"}}, "", true)
	if err == nil {
		t.Fatal("--era takes its sets from --format; alone it means nothing and must be refused")
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Errorf("the refusal should point at --format, got %q", err)
	}
}

func TestExplicitSetsWinOverTheEra(t *testing.T) {
	o, err := ApplyFormat(Options{Sets: []string{"4ed"}}, "premodern", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if len(o.Sets) != 1 || o.Sets[0] != "4ed" {
		t.Errorf("an explicit --sets must win over --era, got %v", o.Sets)
	}
	if o.Legal != "premodern" {
		t.Errorf("--format should still supply the legality, got %q", o.Legal)
	}
}

func TestUnknownFormatIsRejectedBeforeAnythingDownloads(t *testing.T) {
	_, err := ApplyFormat(Options{}, "premodren", false)
	if err == nil {
		t.Fatal("a misspelled format must be rejected")
	}
	if !strings.Contains(err.Error(), "premodern") {
		t.Errorf("the error should list what is available, got %q", err)
	}
}

func TestUnknownFormatErrorListsEveryFormat(t *testing.T) {
	_, err := ApplyFormat(Options{}, "moddern", false)
	if err == nil {
		t.Fatal("a misspelled format must be rejected")
	}
	for _, name := range knownFormats {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error should list %q, got %q", name, err)
		}
	}
}

func TestNoFormatLeavesOptionsAlone(t *testing.T) {
	in := Options{Rarities: []string{"rare"}, Since: 2020}
	o, err := ApplyFormat(in, "", false)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "" || len(o.Sets) != 0 || o.Since != 2020 {
		t.Errorf("an empty --format must change nothing, got %+v", o)
	}
}

func TestFormatShorthandIsCaseAndSpaceInsensitive(t *testing.T) {
	o, err := ApplyFormat(Options{}, "  Legacy ", false)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "legacy" {
		t.Errorf("Legal = %q, want legacy", o.Legal)
	}

	if o, err = ApplyFormat(Options{}, " PreModern ", true); err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if len(o.Sets) != 29 {
		t.Errorf("--era should key off the normalised name, got %d sets", len(o.Sets))
	}
}
