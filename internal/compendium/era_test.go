package compendium

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

const predhJSONL = `{"object":"card","id":"e-solring-lea","name":"Sol Ring","set":"lea","set_name":"Limited Edition Alpha","collector_number":"263","released_at":"1993-08-05","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/lea/263","prices":{"usd":"3000.00"},"legalities":{"predh":"legal","premodern":"not_legal"}}
{"object":"card","id":"e-solring-cmd","name":"Sol Ring","set":"cmd","set_name":"Commander 2011","collector_number":"246","released_at":"2011-06-17","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/cmd/246","prices":{"usd":"2.00"},"legalities":{"predh":"legal","premodern":"not_legal"}}
{"object":"card","id":"e-batterskull","name":"Batterskull","set":"nph","set_name":"New Phyrexia","collector_number":"128","released_at":"2011-05-13","rarity":"mythic","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/nph/128","prices":{"usd":"5.00"},"legalities":{"predh":"legal","premodern":"not_legal"}}
{"object":"card","id":"e-blaze-por","name":"Blaze","set":"por","set_name":"Portal","collector_number":"120","released_at":"1997-05-01","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/por/120","prices":{"usd":"0.50"},"legalities":{"predh":"legal","premodern":"not_legal"}}
{"object":"card","id":"e-snapcaster","name":"Snapcaster Mage","set":"isd","set_name":"Innistrad","collector_number":"78","released_at":"2011-09-30","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/isd/78","prices":{"usd":"20.00"},"legalities":{"predh":"not_legal","premodern":"not_legal"}}
{"object":"card","id":"e-undated","name":"Undated Oddity","set":"pmei","set_name":"Media Inserts","collector_number":"1","released_at":"","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/pmei/1","prices":{"usd":"1.00"},"legalities":{"predh":"legal","premodern":"not_legal"}}
`

func buildEra(t *testing.T, o Options) *store.Store {
	t.Helper()
	prices, _, _ := allPrices(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	o.Days = 30
	o.BulkListingURL = serveScryfall(t, predhJSONL)
	o.PriceBaseURL = serveMTGJSON(t, prices)
	o.TCGCSVBaseURL = offlineTCGCSV
	o.CacheDir = t.TempDir()
	if _, err := Build(context.Background(), st, o, progress.Fn(func(progress.Event) {})); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return st
}

func TestPredhEraDropsPrintingsFromCommander2011Onward(t *testing.T) {
	st := buildEra(t, Options{Legal: "predh", Before: "2011-06-17"})

	want := []string{"Batterskull", "Blaze", "Sol Ring"}
	if got := seededNames(t, st); !slices.Equal(got, want) {
		t.Errorf("seeded %v, want %v\n"+
			"  the Commander 2011 Sol Ring reports predh:legal because legality is per card, "+
			"but that printing is the boundary the era exists to cut;\n"+
			"  New Phyrexia (2011-05-13) is the last set inside it, and Portal belongs to the "+
			"PreDH pool even though Premodern curates it out;\n"+
			"  a printing with no release date cannot be shown to be inside the era, so it goes",
			got, want)
	}
}

func TestPredhWithoutTheEraKeepsEveryLegalPrinting(t *testing.T) {
	st := buildEra(t, Options{Legal: "predh"})

	want := []string{"Batterskull", "Blaze", "Sol Ring", "Sol Ring", "Undated Oddity"}
	if got := seededNames(t, st); !slices.Equal(got, want) {
		t.Errorf("seeded %v, want %v; without --era every legal printing stays, "+
			"reprints included", got, want)
	}
}

func TestPredhEraStopsAtCommander2011(t *testing.T) {
	o, err := ApplyFormat(Options{}, "predh", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Legal != "predh" {
		t.Errorf("Legal = %q, want predh", o.Legal)
	}
	if o.Before != "2011-06-17" {
		t.Errorf("Before = %q, want 2011-06-17, the day Commander 2011 landed and the "+
			"PreDH pool closed", o.Before)
	}
	if len(o.Sets) != 0 {
		t.Errorf("PreDH takes every paper set before its cutoff, Portal and Starter "+
			"included, so it is a date bound and not a set list; got %v", o.Sets)
	}
}

func TestPredhWithoutTheEraFlagBoundsNothing(t *testing.T) {
	o, err := ApplyFormat(Options{}, "predh", false)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Before != "" || len(o.Sets) != 0 {
		t.Errorf("--format predh alone must filter on legality only, got Before=%q Sets=%v",
			o.Before, o.Sets)
	}
}

func TestPremodernEraIsASetListAndNotADate(t *testing.T) {
	o, err := ApplyFormat(Options{}, "premodern", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if o.Before != "" {
		t.Errorf("Premodern curates Portal and the Starter sets out of a window it would "+
			"otherwise cover, so its era is a set list; got Before=%q", o.Before)
	}
	if len(o.Sets) != 29 {
		t.Errorf("got %d sets, want 29", len(o.Sets))
	}
}

func TestTheRefusalNamesEveryFormatThatHasAnEra(t *testing.T) {
	_, err := ApplyFormat(Options{}, "legacy", true)
	if err == nil {
		t.Fatal("legacy has no era and must be refused")
	}
	for _, want := range []string{"predh", "premodern"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %q as a format that does have an era, got %q",
				want, err)
		}
	}
}
