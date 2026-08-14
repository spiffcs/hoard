package command

import (
	"context"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

type layeredSearcher struct {
	local  tui.Searcher
	remote tui.Searcher
}

func newSearcher(cat *catalog.Catalog) tui.Searcher {
	s := layeredSearcher{remote: scryfallSearcher{}}

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

var _ tui.BlockSearcher = layeredSearcher{}

func (s layeredSearcher) PrintBySetNumberLang(ctx context.Context, set, number, lang string) (*scryfall.Card, error) {

	byBlock, ok := s.local.(tui.BlockSearcher)
	if !ok {
		return nil, nil
	}
	return byBlock.PrintBySetNumberLang(ctx, set, number, lang)
}

func (s layeredSearcher) NamedFuzzyLocal(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	if s.local == nil {
		return s.remote.NamedFuzzy(ctx, text)
	}
	return s.local.NamedFuzzy(ctx, text)
}

type scryfallSearcher struct{}

func (scryfallSearcher) Autocomplete(ctx context.Context, q string) ([]string, error) {
	return scryfall.Autocomplete(ctx, q)
}
func (scryfallSearcher) SearchPrints(ctx context.Context, name string) ([]scryfall.Card, error) {
	return scryfall.SearchPrints(ctx, name)
}

func (scryfallSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	card, err := scryfall.NamedFuzzy(ctx, text)
	if err != nil || card == nil {
		return card, cardname.Match{}, err
	}
	n, c := cardname.Normalize(text), cardname.Normalize(card.Name)
	if !cardname.Plausible(text, card.Name) {

		if cardname.PrefixCandidate(text, card.Name) {
			return card, cardname.Match{Similarity: cardname.Similarity(n, c), PrefixOnly: true}, nil
		}
		return nil, cardname.Match{}, nil
	}
	return card, cardname.Match{Exact: n == c, Similarity: cardname.Similarity(n, c)}, nil
}
