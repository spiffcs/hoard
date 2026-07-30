package main

import (
	"context"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

// layeredSearcher answers card lookups locally where it can and from Scryfall
// where it cannot, holding the whole cache policy in one place.
//
// The rule: an empty local result is a miss, not an answer, and a miss goes to
// the API — which is what keeps a card printed since the last build addable.
//
// local is nil when there is no usable catalog, making this exactly the Scryfall
// client hoard always used. It is an interface rather than the concrete catalog so
// the policy can be tested against fakes.
type layeredSearcher struct {
	local  tui.Searcher
	remote tui.Searcher
}

// newSearcher composes the two, given a catalog that may be nil or empty.
func newSearcher(cat *catalog.Catalog) tui.Searcher {
	s := layeredSearcher{remote: scryfallSearcher{}}
	// An empty catalog is worse than no catalog: every lookup would miss, pay
	// for a query, and go to the API anyway.
	if cat != nil && cat.CardCount() > 0 {
		s.local = cat
	}
	return s
}

func (s layeredSearcher) Autocomplete(ctx context.Context, q string) ([]string, error) {
	if s.local != nil {
		if names, err := s.local.Autocomplete(ctx, q); err == nil && len(names) > 0 {
			return names, nil
		}
	}
	return s.remote.Autocomplete(ctx, q)
}

func (s layeredSearcher) SearchPrints(ctx context.Context, name string) ([]scryfall.Card, error) {
	if s.local != nil {
		if cards, err := s.local.SearchPrints(ctx, name); err == nil && len(cards) > 0 {
			return cards, nil
		}
	}
	return s.remote.SearchPrints(ctx, name)
}

// NamedFuzzy is the one lookup where a local miss is meaningful.
//
// The local matcher refuses text it cannot explain — that is its job, and the
// refusal is what stops a keycap or a line of rules text becoming a card. Asking
// Scryfall afterwards would hand exactly those cases to the endpoint that
// resolves "option" to "Opt", reintroducing the bug the check exists to prevent.
//
// So the API is consulted only when the catalog had nothing to match *against*:
// a name it has never seen, which means a printing newer than the last build.
// A confident local "no" is final.
func (s layeredSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, error) {
	if s.local == nil {
		return s.remote.NamedFuzzy(ctx, text)
	}
	card, err := s.local.NamedFuzzy(ctx, text)
	if err == nil && card != nil {
		return card, nil
	}
	if err != nil {
		return s.remote.NamedFuzzy(ctx, text)
	}
	return nil, nil
}

// scryfallSearcher adapts the package-level scryfall functions to tui.Searcher.
// It lives here, beside the layering policy, so internal/tui itself never
// talks to the network.
type scryfallSearcher struct{}

func (scryfallSearcher) Autocomplete(ctx context.Context, q string) ([]string, error) {
	return scryfall.Autocomplete(ctx, q)
}
func (scryfallSearcher) SearchPrints(ctx context.Context, name string) ([]scryfall.Card, error) {
	return scryfall.SearchPrints(ctx, name)
}
func (scryfallSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, error) {
	return scryfall.NamedFuzzy(ctx, text)
}
