package compendium

import (
	"slices"
	"strings"
	"testing"
)

func TestPremodernFormatImpliesLegalityAndTheEraSets(t *testing.T) {
	o, err := ApplyFormat(Options{}, "premodern")
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "premodern" {
		t.Errorf("Legal = %q, want premodern", o.Legal)
	}
	if len(o.Sets) != 29 {
		t.Errorf("got %d sets, want the 29 from Fourth Edition through Scourge: %v", len(o.Sets), o.Sets)
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

func TestFormatDoesNotOverrideFlagsYouGave(t *testing.T) {
	o, err := ApplyFormat(Options{Sets: []string{"4ed"}}, "premodern")
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if len(o.Sets) != 1 || o.Sets[0] != "4ed" {
		t.Errorf("an explicit -sets must win over the shorthand, got %v", o.Sets)
	}
	if o.Legal != "premodern" {
		t.Errorf("the shorthand should still supply the legality, got %q", o.Legal)
	}

	if o, err = ApplyFormat(Options{Legal: "legacy"}, "premodern"); err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "legacy" {
		t.Errorf("an explicit -legal must win over the shorthand, got %q", o.Legal)
	}
}

func TestUnknownFormatIsRejectedBeforeAnythingDownloads(t *testing.T) {
	_, err := ApplyFormat(Options{}, "premodren")
	if err == nil {
		t.Fatal("a misspelled format must be rejected")
	}
	if !strings.Contains(err.Error(), "premodern") {
		t.Errorf("the error should list what is available, got %q", err)
	}
}

func TestNoFormatLeavesOptionsAlone(t *testing.T) {
	in := Options{Rarities: []string{"rare"}, Since: 2020}
	o, err := ApplyFormat(in, "")
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "" || len(o.Sets) != 0 || o.Since != 2020 {
		t.Errorf("an empty -format must change nothing, got %+v", o)
	}
}
