package compendium

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

const tokenJSONL = `{"object":"card","id":"c-real","name":"Ragavan, Nimble Pilferer","layout":"normal","set":"mh2","set_name":"Modern Horizons 2","set_type":"draft_innovation","collector_number":"138","released_at":"2021-06-18","rarity":"mythic","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mh2/138","prices":{"usd":"60.00"}}
{"object":"card","id":"c-real-2","name":"Sol Ring","layout":"normal","set":"c21","set_name":"Commander 2021","set_type":"commander","collector_number":"1","released_at":"2021-04-23","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/c21/1","prices":{"usd":"2.00"}}
{"object":"card","id":"t-token","name":"Treasure","layout":"token","set":"tmh2","set_name":"Modern Horizons 2 Tokens","set_type":"token","collector_number":"12","released_at":"2021-06-18","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/tmh2/12","prices":{"usd":"0.25"}}
{"object":"card","id":"t-dfc","name":"Goblin // Boar","layout":"double_faced_token","set":"tmh2","set_name":"Modern Horizons 2 Tokens","set_type":"token","collector_number":"13","released_at":"2021-06-18","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/tmh2/13","prices":{"usd":"0.30"}}
{"object":"card","id":"t-unlabelled","name":"Zombie","set":"tmh2","set_name":"Modern Horizons 2 Tokens","set_type":"token","collector_number":"14","released_at":"2021-06-18","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/tmh2/14","prices":{"usd":"0.20"}}
{"object":"card","id":"e-emblem","name":"Wrenn and Six Emblem","layout":"emblem","set":"tmh1","set_name":"Modern Horizons Tokens","set_type":"token","collector_number":"20","released_at":"2019-06-14","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/tmh1/20","prices":{"usd":"3.00"}}
{"object":"card","id":"a-art","name":"Sea Gate Restoration","layout":"art_series","set":"amh2","set_name":"Modern Horizons 2 Art Series","set_type":"memorabilia","collector_number":"5","released_at":"2021-06-18","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/amh2/5","prices":{"usd":"1.50"}}
{"object":"card","id":"f-front","name":"Runaways","layout":"front_card","set":"fmsc","set_name":"Marvel Super Heroes Jumpstart Front Cards","set_type":"memorabilia","collector_number":"44","released_at":"2026-06-26","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"type_line":"Card","oracle_text":"(Theme color: {R})","scryfall_uri":"https://scryfall.com/card/fmsc/44/runaways","prices":{"usd":null,"usd_foil":null,"usd_etched":null}}
{"object":"card","id":"f-front-priced","name":"Detective","layout":"front_card","set":"fj22","set_name":"Jumpstart 2022 Front Cards","set_type":"memorabilia","collector_number":"10","released_at":"2022-12-02","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"type_line":"Card","oracle_text":"(Theme color: {U})","scryfall_uri":"https://scryfall.com/card/fj22/10","prices":{"usd":"0.40"}}
`

func TestBuildNeverSeedsTokensEmblemsArtOrFrontCards(t *testing.T) {
	prices, _, _ := allPrices(t)
	want := []string{"Ragavan, Nimble Pilferer", "Sol Ring"}

	for _, tc := range []struct {
		name string
		opts Options
	}{
		{name: "no filter at all"},
		{name: "since a year", opts: Options{Since: 2021}},
		{name: "a rarity every token shares", opts: Options{Rarities: []string{"common", "mythic", "uncommon"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { st.Close() })

			o := tc.opts
			o.Days = 30
			o.BulkListingURL = serveScryfall(t, tokenJSONL)
			o.PriceBaseURL = serveMTGJSON(t, prices)
			o.TCGCSVBaseURL = offlineTCGCSV
			o.CacheDir = t.TempDir()

			res, err := Build(context.Background(), st, o, progress.Fn(func(progress.Event) {}))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			if got := seededNames(t, st); !slices.Equal(got, want) {
				t.Errorf("seeded %v, want %v", got, want)
			}
			if res.Printings != len(want) {
				t.Errorf("Result.Printings = %d, want %d", res.Printings, len(want))
			}
		})
	}
}

func TestBuildKeepsARarityFilterWorkingOnRealCards(t *testing.T) {
	prices, _, _ := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if _, err := Build(context.Background(), st, Options{
		Days:           30,
		Rarities:       []string{"uncommon"},
		BulkListingURL: serveScryfall(t, tokenJSONL),
		PriceBaseURL:   serveMTGJSON(t, prices),
		TCGCSVBaseURL:  offlineTCGCSV,
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(progress.Event) {})); err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []string{"Sol Ring"}
	if got := seededNames(t, st); !slices.Equal(got, want) {
		t.Errorf("seeded %v, want %v", got, want)
	}
}
