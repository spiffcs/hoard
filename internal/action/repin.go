package action

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

type PrintSearcher interface {
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)
}

type RepinResult struct {
	DeckID  int64
	Deck    string
	SetCode string

	Total   int
	Already int

	Repinned int
	Moved    int

	Missing []string
}

func RepinDeck(ctx context.Context, st *store.Store, prints PrintSearcher, deckRef, setCode string) (RepinResult, error) {
	var res RepinResult
	setCode = strings.ToLower(strings.TrimSpace(setCode))
	if setCode == "" {
		return res, fmt.Errorf("say which set to re-pin to, like cma")
	}
	deck, err := st.DeckByRef(deckRef)
	if err != nil {
		return res, err
	}
	res.DeckID, res.Deck, res.SetCode = deck.ID, deck.Name, setCode
	entries, err := st.DeckEntries(deck.ID)
	if err != nil {
		return res, err
	}

	type printing struct{ name, set string }
	seen := map[string]printing{}
	for _, e := range entries {
		seen[e.Card.ScryfallID] = printing{name: e.Card.Name, set: e.Card.SetCode}
	}
	res.Total = len(seen)

	mapping := map[string]string{}
	var targets []scryfall.Card
	upserted := map[string]bool{}
	for sid, p := range seen {
		if strings.EqualFold(p.set, setCode) {
			res.Already++
			continue
		}
		found, err := prints.SearchPrints(ctx, p.name)
		if err != nil {
			return res, fmt.Errorf("looking up printings of %q: %w", p.name, err)
		}
		pick, ok := lowestInSet(found, setCode)
		if !ok {
			res.Missing = append(res.Missing, p.name)
			continue
		}
		mapping[sid] = pick.ID
		if !upserted[pick.ID] {
			upserted[pick.ID] = true
			targets = append(targets, pick)
		}
	}
	if len(mapping) == 0 {
		return res, nil
	}

	if err := st.UpsertPrintings(targets); err != nil {
		return res, err
	}
	moved, err := st.RepointDeckPrintings(deck.ID, mapping)
	if err != nil {
		return res, err
	}
	res.Repinned, res.Moved = len(mapping), moved
	return res, nil
}

func lowestInSet(prints []scryfall.Card, setCode string) (scryfall.Card, bool) {
	var best scryfall.Card
	found := false
	for _, p := range prints {
		if !strings.EqualFold(p.Set, setCode) {
			continue
		}
		if !found || collectorLess(p.CollectorNumber, best.CollectorNumber) {
			best, found = p, true
		}
	}
	return best, found
}

func collectorLess(a, b string) bool {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return an < bn
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a < b
}
