package main

import (
	"context"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

// layeredSearcher answers card lookups locally where it can and from Scryfall
// where it cannot.
//
// One type holds the whole cache policy, so there is a single place that decides
// what "the catalog does not have this" means. The rule is the same for all
// three lookups: an empty local result is not an answer, it is a miss, and a
// miss goes to the API. That keeps a card printed since the last build addable,
// which a cache treated as authoritative would not.
//
// local may be nil — no writable cache directory, a catalog that would not open,
// or one that has never been built — in which case this is exactly the Scryfall
// client hoard always used. It is the same interface as remote rather than the
// concrete catalog, so the policy below can be exercised against fakes without a
// database.
type layeredSearcher struct {
	local  tui.Searcher
	remote tui.Searcher
}

// newSearcher composes the two, given a catalog that may be nil or empty.
func newSearcher(cat *catalog.Catalog) tui.Searcher {
	s := layeredSearcher{remote: tui.NewScryfallSearcher()}
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
