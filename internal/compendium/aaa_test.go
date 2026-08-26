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

const aaaJSONL = `{"object":"card","id":"a-lotus-lea","name":"Black Lotus","set":"lea","set_name":"Limited Edition Alpha","collector_number":"232","released_at":"1993-08-05","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/lea/232","prices":{"usd":"25000.00"},"legalities":{"oldschool":"legal","vintage":"restricted"}}
{"object":"card","id":"a-mindtwist-lea","name":"Mind Twist","set":"lea","set_name":"Limited Edition Alpha","collector_number":"16","released_at":"1993-08-05","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/lea/16","prices":{"usd":"400.00"},"legalities":{"oldschool":"legal","vintage":"legal"}}
{"object":"card","id":"a-mindtwist-4ed","name":"Mind Twist","set":"4ed","set_name":"Fourth Edition","collector_number":"22","released_at":"1995-04-01","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/4ed/22","prices":{"usd":"3.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-karplusan-ice","name":"Karplusan Forest","set":"ice","set_name":"Ice Age","collector_number":"357","released_at":"1995-06-03","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/ice/357","prices":{"usd":"4.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-glaciers-all","name":"Thawing Glaciers","set":"all","set_name":"Alliances","collector_number":"139","released_at":"1996-06-10","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/all/139","prices":{"usd":"12.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-shivanreef-apc","name":"Shivan Reef","set":"apc","set_name":"Apocalypse","collector_number":"144","released_at":"2001-06-04","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/apc/144","prices":{"usd":"6.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-forge-apc","name":"Battlefield Forge","set":"apc","set_name":"Apocalypse","collector_number":"140","released_at":"2001-06-04","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/apc/140","prices":{"usd":"5.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-vindicate-apc","name":"Vindicate","set":"apc","set_name":"Apocalypse","collector_number":"105","released_at":"2001-06-04","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/apc/105","prices":{"usd":"8.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-serra-chr","name":"Serra Angel","set":"chr","set_name":"Chronicles","collector_number":"9","released_at":"1995-07-01","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/chr/9","prices":{"usd":"2.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-solring-cmd","name":"Sol Ring","set":"cmd","set_name":"Commander 2011","collector_number":"246","released_at":"2011-06-17","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/cmd/246","prices":{"usd":"2.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
{"object":"card","id":"a-snapcaster-isd","name":"Snapcaster Mage","set":"isd","set_name":"Innistrad","collector_number":"78","released_at":"2011-09-30","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/isd/78","prices":{"usd":"20.00"},"legalities":{"oldschool":"not_legal","vintage":"legal"}}
`

func buildAAA(t *testing.T, o Options) *store.Store {
	t.Helper()
	prices, _, _ := allPrices(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "ante.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	o.Days = 30
	o.BulkListingURL = serveScryfall(t, aaaJSONL)
	o.PriceBaseURL = serveMTGJSON(t, prices)
	o.CacheDir = t.TempDir()
	if _, err := Build(context.Background(), st, o, progress.Fn(func(progress.Event) {})); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return st
}

func TestAAAEraIsTheEbonAntePool(t *testing.T) {
	o, err := ApplyFormat(Options{}, "aaa", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}

	wantSets := []string{
		"lea", "leb", "2ed", "3ed", "4ed", "arn", "atq", "leg",
		"drk", "fem", "hml", "ice", "all",
	}
	if !slices.Equal(o.Sets, wantSets) {
		t.Errorf("Sets = %v, want %v; Ebon Ante takes thirteen whole sets, Alpha through "+
			"Alliances, and Apocalypse is not one of them because only five of its cards "+
			"are legal", o.Sets, wantSets)
	}

	wantOnly := []string{
		"Battlefield Forge", "Caves of Koilos", "Llanowar Wastes",
		"Shivan Reef", "Yavimaya Coast",
	}
	if got := o.Only["apc"]; !slices.Equal(got, wantOnly) {
		t.Errorf("Only[apc] = %v, want %v; the format grants Apocalypse's five enemy "+
			"painlands and nothing else, which alongside the Ice Age five is every "+
			"dual painland", got, wantOnly)
	}
	if len(o.Only) != 1 {
		t.Errorf("Apocalypse is the only partial set in the pool, got %v", o.Only)
	}

	if want := []string{"Mind Twist"}; !slices.Equal(o.Except, want) {
		t.Errorf("Except = %v, want %v; Mind Twist is the format's one ban", o.Except, want)
	}

	if o.Legal != "" {
		t.Errorf("Legal = %q, want empty; Scryfall records no legality for Ebon Ante, so "+
			"filtering on a legality key would match nothing at all", o.Legal)
	}
	if o.Before != "" {
		t.Errorf("Before = %q, want empty; the pool is a curated set list, not a date "+
			"cutoff — Chronicles and Portal fall inside the window and are out", o.Before)
	}
}

func TestAAAEraBuildsTheEbonAntePool(t *testing.T) {
	o, err := ApplyFormat(Options{}, "aaa", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	st := buildAAA(t, o)

	want := []string{
		"Battlefield Forge", "Black Lotus", "Karplusan Forest",
		"Shivan Reef", "Thawing Glaciers",
	}
	if got := seededNames(t, st); !slices.Equal(got, want) {
		t.Errorf("seeded %v, want %v\n"+
			"  both Mind Twists go, in Alpha and in Fourth Edition, because the ban is on "+
			"the card and not on one printing of it;\n"+
			"  Vindicate goes because Apocalypse is in the pool for five painlands only;\n"+
			"  Chronicles' Serra Angel goes because the pool is a set list and Chronicles "+
			"is not on it, old frame or not;\n"+
			"  Commander 2011 and Innistrad were never close to it",
			got, want)
	}
}

func TestAAAWithoutTheEraFlagIsRefused(t *testing.T) {
	_, err := ApplyFormat(Options{}, "aaa", false)
	if err == nil {
		t.Fatal("--format aaa on its own must be refused: Scryfall has no aaa legality " +
			"key, so the build would silently filter on nothing")
	}
	if !strings.Contains(err.Error(), "--era") {
		t.Errorf("the refusal should point at --era as the fix, got %q", err)
	}
}

func TestTheRefusalNamesAAAAsAFormatWithAnEra(t *testing.T) {
	_, err := ApplyFormat(Options{}, "legacy", true)
	if err == nil {
		t.Fatal("legacy has no era and must be refused")
	}
	if !strings.Contains(err.Error(), "aaa") {
		t.Errorf("the refusal lists the formats that do have an era and must now name "+
			"aaa, got %q", err)
	}
}

func TestExplicitSetsWinOverTheAAAEraButTheBanHolds(t *testing.T) {
	o, err := ApplyFormat(Options{Sets: []string{"arn"}}, "aaa", true)
	if err != nil {
		t.Fatalf("ApplyFormat: %v", err)
	}
	if want := []string{"arn"}; !slices.Equal(o.Sets, want) {
		t.Errorf("Sets = %v, want %v; an explicit --sets pins a subset of the era by hand",
			o.Sets, want)
	}
	if want := []string{"Mind Twist"}; !slices.Equal(o.Except, want) {
		t.Errorf("Except = %v, want %v; narrowing the sets does not unban a card",
			o.Except, want)
	}
	if len(o.Only) != 0 {
		t.Errorf("Only = %v, want none; --sets replaced the era's set list, so the "+
			"Apocalypse carve-out has no set to carve", o.Only)
	}
}
