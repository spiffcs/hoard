package catalogdb

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

const rarityJSONL = `{"object":"card","id":"r-mythic","name":"Ragavan, Nimble Pilferer","set":"mh2","set_name":"Modern Horizons 2","collector_number":"138","released_at":"2021-06-18","rarity":"mythic","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mh2/138","prices":{"usd":"60.00"}}
{"object":"card","id":"r-rare","name":"Urza's Saga","set":"mh2","set_name":"Modern Horizons 2","collector_number":"259","released_at":"2021-06-18","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mh2/259","prices":{"usd":"40.00"}}
{"object":"card","id":"r-uncommon","name":"Sol Ring","set":"c21","set_name":"Commander 2021","collector_number":"1","released_at":"2021-04-23","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/c21/1","prices":{"usd":"2.00"}}
{"object":"card","id":"r-common","name":"Lightning Bolt","set":"c21","set_name":"Commander 2021","collector_number":"2","released_at":"2021-04-23","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/c21/2","prices":{"usd":"1.00"}}
{"object":"card","id":"r-blank","name":"No Rarity","set":"c21","set_name":"Commander 2021","collector_number":"3","released_at":"2021-04-23","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/c21/3","prices":{"usd":"1.00"}}
`

func seededNames(t *testing.T, st *store.Store) []string {
	t.Helper()
	rows, err := st.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	slices.Sort(out)
	return out
}

func TestRarityFilterKeepsOnlyTheWantedRarities(t *testing.T) {
	prices, _, _ := allPrices(t)

	for _, tc := range []struct {
		name     string
		rarities []string
		want     []string
	}{
		{
			name:     "mythics and rares",
			rarities: []string{"mythic", "rare"},
			want:     []string{"Ragavan, Nimble Pilferer", "Urza's Saga"},
		},
		{
			name:     "uncommons only",
			rarities: []string{"uncommon"},
			want:     []string{"Sol Ring"},
		},
		{
			name:     "mixed case and spacing is accepted",
			rarities: []string{" Mythic ", "RARE"},
			want:     []string{"Ragavan, Nimble Pilferer", "Urza's Saga"},
		},
		{
			name:     "no filter keeps every paper printing",
			rarities: nil,
			want: []string{"Lightning Bolt", "No Rarity", "Ragavan, Nimble Pilferer",
				"Sol Ring", "Urza's Saga"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { st.Close() })

			res, err := Build(context.Background(), st, Options{
				Days:           30,
				Rarities:       tc.rarities,
				BulkListingURL: serveScryfall(t, rarityJSONL),
				PriceBaseURL:   serveMTGJSON(t, prices),
				CacheDir:       t.TempDir(),
			}, progress.Fn(func(progress.Event) {}))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			if got := seededNames(t, st); !slices.Equal(got, tc.want) {
				t.Errorf("seeded %v, want %v", got, tc.want)
			}
			if res.Printings != len(tc.want) {
				t.Errorf("Result.Printings = %d, want %d", res.Printings, len(tc.want))
			}
		})
	}
}

func TestRarityFilterRejectsAnUnknownRarity(t *testing.T) {
	prices, _, _ := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	_, err = Build(context.Background(), st, Options{
		Days:           30,
		Rarities:       []string{"mythic", "legendary"},
		BulkListingURL: serveScryfall(t, rarityJSONL),
		PriceBaseURL:   serveMTGJSON(t, prices),
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(progress.Event) {}))

	if err == nil {
		t.Fatal("Build accepted rarity \"legendary\"; a typo must not silently seed nothing")
	}
	if got := err.Error(); !strings.Contains(got, "legendary") {
		t.Errorf("error = %q, want it to name the offending rarity", got)
	}
}
