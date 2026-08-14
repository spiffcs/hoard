package command

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

type plainLocal struct{}

func (plainLocal) Autocomplete(context.Context, string) ([]string, error) {
	return nil, nil
}
func (plainLocal) SearchPrints(context.Context, string) ([]scryfall.Card, error) {
	return nil, nil
}
func (plainLocal) NamedFuzzy(context.Context, string) (*scryfall.Card, cardname.Match, error) {
	return nil, cardname.Match{}, nil
}

type blockLocal struct {
	plainLocal
	gotSet, gotNumber, gotLang string
	card                       *scryfall.Card
}

func (b *blockLocal) PrintBySetNumberLang(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
	b.gotSet, b.gotNumber, b.gotLang = set, number, lang
	return b.card, nil
}

func TestLayeredSearcherResolvesBlock(t *testing.T) {
	want := scryfall.Card{Name: "Godless Shrine", Set: "eoe", CollectorNumber: "375", Lang: "en"}
	local := &blockLocal{card: &want}
	s := layeredSearcher{local: local, remote: plainLocal{}}

	got, err := s.PrintBySetNumberLang(t.Context(), "EOE", "375", "en")
	if err != nil {
		t.Fatalf("PrintBySetNumberLang: %v", err)
	}
	if got == nil || got.Name != want.Name {
		t.Fatalf("got %+v, want %q", got, want.Name)
	}

	if local.gotSet != "EOE" || local.gotNumber != "375" || local.gotLang != "en" {
		t.Errorf("local saw %q/%q/%q, want EOE/375/en",
			local.gotSet, local.gotNumber, local.gotLang)
	}
}

func TestLayeredSearcherBlockWithoutCapability(t *testing.T) {
	for name, s := range map[string]layeredSearcher{
		"no catalog":    {remote: plainLocal{}},
		"plain catalog": {local: plainLocal{}, remote: plainLocal{}},
	} {
		t.Run(name, func(t *testing.T) {
			card, err := s.PrintBySetNumberLang(t.Context(), "EOE", "375", "en")
			if err != nil || card != nil {
				t.Errorf("got %+v, %v; want nil, nil", card, err)
			}
		})
	}
}

func TestNewSearcherOffersBlockLookup(t *testing.T) {
	if _, ok := newSearcher(nil).(tui.BlockSearcher); !ok {
		t.Error("newSearcher result does not implement tui.BlockSearcher; " +
			"the collector-block rescue is dead in production")
	}
}
