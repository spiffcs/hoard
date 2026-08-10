package command

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

// plainLocal is a catalog-shaped local layer with no block lookup, standing in
// for a searcher that cannot answer from a collector block.
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

// blockLocal adds the block lookup, recording what it was handed so the test
// can prove the arguments survived the hop.
type blockLocal struct {
	plainLocal
	gotSet, gotNumber, gotLang string
	card                       *scryfall.Card
}

func (b *blockLocal) PrintBySetNumberLang(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
	b.gotSet, b.gotNumber, b.gotLang = set, number, lang
	return b.card, nil
}

// TestLayeredSearcherResolvesBlock pins the collector-block rescue to the
// searcher production actually builds.
//
// The path is reached through a runtime type assertion in resolveByBlock, so
// an adapter that stops implementing it fails silently rather than failing to
// build. That happened: the lookup grew a lang parameter, this adapter kept
// the two-argument method, and every card whose title would not read was
// dropped as unreadable for 32 commits — including a Godless Shrine that had
// read a clean EOE/375/en off its own footer.
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
	// The language is the whole reason this signature changed: it is what
	// separates a foreign-only printing from its English namesake under the
	// same set and number, so dropping it on the way through is the bug.
	if local.gotSet != "EOE" || local.gotNumber != "375" || local.gotLang != "en" {
		t.Errorf("local saw %q/%q/%q, want EOE/375/en",
			local.gotSet, local.gotNumber, local.gotLang)
	}
}

// TestLayeredSearcherBlockWithoutCapability covers the two ways there is no
// block lookup to reach: no catalog at all, and a local layer that does not
// offer one. Both decline quietly — the scan flow treats a nil card as "not
// identified from the block" and carries on.
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

// TestNewSearcherOffersBlockLookup is the assertion that would have caught the
// drift from the outside: whatever newSearcher composes must still satisfy the
// capability the scan flow probes for.
//
// The var _ tui.BlockSearcher line in searcher.go pins the method set at
// compile time; this pins the constructor's actual return value, which is what
// resolveByBlock receives.
func TestNewSearcherOffersBlockLookup(t *testing.T) {
	if _, ok := newSearcher(nil).(tui.BlockSearcher); !ok {
		t.Error("newSearcher result does not implement tui.BlockSearcher; " +
			"the collector-block rescue is dead in production")
	}
}
