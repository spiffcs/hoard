package scryfall

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	slowGap = 0
	defaultGap = 0
	os.Exit(m.Run())
}

func TestEndpointClass(t *testing.T) {
	slow := []string{
		"https://api.scryfall.com/cards/search?q=x",
		"https://api.scryfall.com/cards/search?unique=prints&page=2",
		"https://api.scryfall.com/cards/named?fuzzy=sol+ring",
		"https://api.scryfall.com/cards/collection",
		"http://127.0.0.1:9999/cards/search?q=x",
	}
	for _, ep := range slow {
		if class, _ := endpointClass(ep); class == "" {
			t.Errorf("endpointClass(%q) fell to the default class; want a 2/second class", ep)
		}
	}
	fast := []string{
		"https://api.scryfall.com/cards/autocomplete?q=sol",
		"https://api.scryfall.com/cards/uma/7",
	}
	for _, ep := range fast {
		if class, _ := endpointClass(ep); class != "" {
			t.Errorf("endpointClass(%q) = %q; want the default class", ep, class)
		}
	}
}

func TestPacerSpacesSlowEndpointRequests(t *testing.T) {
	oldSlow, oldDefault := slowGap, defaultGap
	slowGap, defaultGap = 50*time.Millisecond, 0
	defer func() { slowGap, defaultGap = oldSlow, oldDefault }()

	at := schedulePlan(namedPath, namedPath, namedPath)

	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < slowGap {
			t.Errorf("requests %d and %d are scheduled %v apart; want at least %v",
				i-1, i, gap, slowGap)
		}
	}
}
