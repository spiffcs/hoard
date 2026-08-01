package main

import (
	"context"

	"github.com/spiffcs/hoard/internal/cardname"
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

// NamedFuzzy prefers the catalog and falls through to the API on any local
// miss — including a local refusal. A card from a set newer than the last
// catalog build looks exactly like junk to the catalog (nothing plausible to
// match against), and treating that "no" as final made every newly-released
// card unscannable. What keeps the fallthrough safe from the endpoint that
// resolves "option" to "Opt" is that the remote adapter applies the same
// cardname.Plausible identity check the catalog does — junk dies in either
// layer; a genuinely new card dies only in the stale one. The match rides
// along from whichever layer answered, computed with the same cardname
// functions, so a score means the same thing either way.
func (s layeredSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	if s.local == nil {
		return s.remote.NamedFuzzy(ctx, text)
	}
	card, match, err := s.local.NamedFuzzy(ctx, text)
	if err == nil && card != nil {
		return card, match, nil
	}
	return s.remote.NamedFuzzy(ctx, text)
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

// NamedFuzzy wraps the unchanged API call and computes the match itself, with
// the same cardname functions the catalog uses — the API doesn't report how it
// matched, and inventing a different scoring here would let the two layers
// disagree about what "exact" means.
//
// The Plausible gate makes this an identity check like the catalog's own:
// Scryfall's endpoint is a *search* that resolves "option" to the card "Opt"
// because the query contains the name, which is right for a human typing and
// wrong for OCR. Refusing implausible answers here is what makes it safe for
// the layered searcher to consult the API on every local miss.
func (scryfallSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	card, err := scryfall.NamedFuzzy(ctx, text)
	if err != nil || card == nil {
		return card, cardname.Match{}, err
	}
	if !cardname.Plausible(text, card.Name) {
		return nil, cardname.Match{}, nil
	}
	n, c := cardname.Normalize(text), cardname.Normalize(card.Name)
	return card, cardname.Match{Exact: n == c, Similarity: cardname.Similarity(n, c)}, nil
}
