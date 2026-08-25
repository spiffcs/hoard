package action

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
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

	Undocumented int

	Missing []string
}

func RepinDeck(ctx context.Context, d Deps, prints PrintSearcher, deckRef, setCode string) (RepinResult, error) {
	var res RepinResult
	setCode = strings.ToLower(strings.TrimSpace(setCode))
	if setCode == "" {
		return res, fmt.Errorf("say which set to re-pin to, like cma")
	}
	deck, err := d.Store.DeckByRef(deckRef)
	if err != nil {
		return res, err
	}
	res.DeckID, res.Deck, res.SetCode = deck.ID, deck.Name, setCode
	entries, err := d.Store.DeckEntries(deck.ID)
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

	if err := d.Store.UpsertPrintings(targets); err != nil {
		return res, err
	}
	moved, err := d.Store.RepointDeckPrintings(deck.ID, mapping)
	if err != nil {
		return res, err
	}
	res.Repinned, res.Moved = len(mapping), moved
	res.Undocumented = documentPrintings(ctx, d, targets)
	return res, nil
}

func documentPrintings(ctx context.Context, d Deps, targets []scryfall.Card) int {
	var need []string
	for _, c := range targets {
		if len(c.Raw) == 0 {
			need = append(need, c.ID)
		}
	}
	if len(need) == 0 {
		return 0
	}
	found, _, _, err := RefreshCards(ctx, d, nil, need)
	if err != nil {
		return len(need)
	}
	var documented []scryfall.Card
	for _, c := range found {
		if len(c.Raw) > 0 {
			documented = append(documented, c)
		}
	}
	if err := d.Store.UpsertPrintings(documented); err != nil {
		return len(need)
	}
	return len(need) - len(documented)
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
