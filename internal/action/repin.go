package action

// `hoard deck repin`: re-pointing a deck's cards at the set it came from.
//
// A name-only decklist import resolves each card to whatever printing
// Scryfall answers with — typically the newest — so a Commander Anthology
// precon ends up scattered across twenty sets it was never part of. The
// catalog knows every printing per set; this walks the deck and re-points
// each off-set entry at the named set's printing of the same card.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// PrintSearcher is the one lookup repin needs: every printing of an exact
// name. The catalog satisfies it, as does the CLI's layered searcher, which
// falls through to the Scryfall API when the catalog is missing or stale.
type PrintSearcher interface {
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)
}

// RepinResult is what a repin corrected and what it could not.
type RepinResult struct {
	DeckID  int64
	Deck    string
	SetCode string
	// Total is every distinct printing in the deck; Already the ones that
	// were on the set before the run.
	Total   int
	Already int
	// Repinned counts printings re-pointed; Moved the entry rows behind
	// them (a printing held in two boards moves as two rows).
	Repinned int
	Moved    int
	// Missing names have no printing in the set — left untouched, because
	// re-pointing them anywhere would be inventing an answer.
	Missing []string
}

// RepinDeck re-points every off-set printing in the deck at the given
// set's printing of the same card name. Printings the set never had are
// reported and left alone.
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

	// One decision per distinct printing: DeckEntries is per finish and
	// board, and asking the catalog once per row would repeat both the
	// lookup and the answer.
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

// lowestInSet picks the set's printing with the lowest collector number —
// deterministic, and for basics it lands on the set's first art rather than
// whichever the search ranked. ok=false when the set never printed the card.
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

// collectorLess orders collector numbers numerically where they are
// numbers — "9" before "10" — falling back to the string for the suffixed
// forms ("142a", "★").
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
