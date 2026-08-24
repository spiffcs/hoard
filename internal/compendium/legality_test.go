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

const legalityJSONL = `{"object":"card","id":"l-legal","name":"Counterspell","set":"7ed","set_name":"Seventh Edition","collector_number":"67","released_at":"2001-04-11","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/7ed/67","prices":{"usd":"2.00"},"legalities":{"premodern":"legal","legacy":"legal","vintage":"legal","standard":"not_legal"}}
{"object":"card","id":"l-legal-4ed","name":"Icy Manipulator","set":"4ed","set_name":"Fourth Edition","collector_number":"327","released_at":"1995-04-01","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/4ed/327","prices":{"usd":"1.50"},"legalities":{"premodern":"legal","legacy":"legal","vintage":"legal","standard":"not_legal"}}
{"object":"card","id":"l-banned","name":"Force of Will","set":"all","set_name":"Alliances","collector_number":"28","released_at":"1996-06-10","rarity":"uncommon","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/all/28","prices":{"usd":"70.00"},"legalities":{"premodern":"banned","legacy":"legal","vintage":"legal","standard":"not_legal"}}
{"object":"card","id":"l-restricted","name":"Balance","set":"4ed","set_name":"Fourth Edition","collector_number":"246","released_at":"1995-04-01","rarity":"rare","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/4ed/246","prices":{"usd":"9.00"},"legalities":{"premodern":"banned","legacy":"banned","vintage":"restricted","standard":"not_legal"}}
{"object":"card","id":"l-notlegal","name":"Ragavan, Nimble Pilferer","set":"mh2","set_name":"Modern Horizons 2","collector_number":"138","released_at":"2021-06-18","rarity":"mythic","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/mh2/138","prices":{"usd":"60.00"},"legalities":{"premodern":"not_legal","legacy":"legal","vintage":"legal","standard":"not_legal"}}
{"object":"card","id":"l-nolegalities","name":"Unknown Standing","set":"7ed","set_name":"Seventh Edition","collector_number":"999","released_at":"2001-04-11","rarity":"common","lang":"en","games":["paper"],"finishes":["nonfoil"],"scryfall_uri":"https://scryfall.com/card/7ed/999","prices":{"usd":"0.50"}}
`

func TestLegalFilterKeepsOnlyCardsLegalInTheFormat(t *testing.T) {
	prices, _, _ := allPrices(t)

	for _, tc := range []struct {
		name  string
		legal string
		sets  []string
		want  []string
	}{
		{
			name:  "premodern drops banned and not_legal printings",
			legal: "premodern",
			want:  []string{"Counterspell", "Icy Manipulator"},
		},
		{
			name:  "the format is honoured, not hardcoded",
			legal: "legacy",
			want:  []string{"Counterspell", "Force of Will", "Icy Manipulator", "Ragavan, Nimble Pilferer"},
		},
		{
			name:  "restricted is not legal",
			legal: "vintage",
			want:  []string{"Counterspell", "Force of Will", "Icy Manipulator", "Ragavan, Nimble Pilferer"},
		},
		{
			name:  "mixed case and spacing is accepted",
			legal: " PreModern ",
			want:  []string{"Counterspell", "Icy Manipulator"},
		},
		{
			name:  "it composes with the set filter",
			legal: "premodern",
			sets:  []string{"4ed"},
			want:  []string{"Icy Manipulator"},
		},
		{
			name:  "no filter keeps every paper printing",
			legal: "",
			want: []string{"Balance", "Counterspell", "Force of Will", "Icy Manipulator",
				"Ragavan, Nimble Pilferer", "Unknown Standing"},
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
				Legal:          tc.legal,
				Sets:           tc.sets,
				BulkListingURL: serveScryfall(t, legalityJSONL),
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

func TestLegalFilterRejectsAnUnknownFormat(t *testing.T) {
	prices, _, _ := allPrices(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	_, err = Build(context.Background(), st, Options{
		Days:           30,
		Legal:          "premoderrn",
		BulkListingURL: serveScryfall(t, legalityJSONL),
		PriceBaseURL:   serveMTGJSON(t, prices),
		CacheDir:       t.TempDir(),
	}, progress.Fn(func(progress.Event) {}))

	if err == nil {
		t.Fatal("Build accepted format \"premoderrn\"; a typo must not silently seed nothing")
	}
	if got := err.Error(); !strings.Contains(got, "premoderrn") {
		t.Errorf("error = %q, want it to name the offending format", got)
	}
}
